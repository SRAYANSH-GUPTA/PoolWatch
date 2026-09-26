package analyzer

import (
	"fmt"
	"math"
	"time"

	"poolwatch/internal/domain"
)

const (
	defaultTrendWindow  = 2 * time.Minute
	defaultTrendHorizon = 5 * time.Minute
	minTrendSamples     = 5
	minTrendR2          = 0.6
)

// linearFit computes an ordinary least-squares fit y = slope*x + intercept.
// ok is false when there are fewer than two points, the slices differ in
// length, or all x values are identical. r2 is the coefficient of
// determination; a perfectly flat series is reported as a perfect fit (1).
func linearFit(xs, ys []float64) (slope, intercept, r2 float64, ok bool) {
	n := len(xs)
	if n < 2 || n != len(ys) {
		return 0, 0, 0, false
	}
	var sumX, sumY float64
	for i := 0; i < n; i++ {
		sumX += xs[i]
		sumY += ys[i]
	}
	meanX, meanY := sumX/float64(n), sumY/float64(n)

	var sxx, sxy, syy float64
	for i := 0; i < n; i++ {
		dx, dy := xs[i]-meanX, ys[i]-meanY
		sxx += dx * dx
		sxy += dx * dy
		syy += dy * dy
	}
	if sxx == 0 {
		return 0, 0, 0, false
	}
	slope = sxy / sxx
	intercept = meanY - slope*meanX
	if syy == 0 {
		r2 = 1
	} else {
		r2 = (sxy * sxy) / (sxx * syy)
	}
	return slope, intercept, r2, true
}

// forecastCrossing returns how many x-units (seconds) after atX the fitted
// line slope*x+intercept reaches limit. It returns 0 if the fitted value at
// atX is already at or above limit, and ok=false if the line never rises to it.
func forecastCrossing(slope, intercept, atX, limit float64) (seconds float64, ok bool) {
	current := slope*atX + intercept
	if current >= limit {
		return 0, true
	}
	if slope <= 0 || math.IsNaN(slope) || math.IsInf(slope, 0) {
		return 0, false
	}
	crossX := (limit - intercept) / slope
	return crossX - atX, true
}

// trendPoints returns the snapshots within window ending at current, in
// chronological order, always including current as the last point.
func trendPoints(history []domain.Snapshot, current domain.Snapshot, window time.Duration) []domain.Snapshot {
	cutoff := current.Timestamp.Add(-window)
	points := make([]domain.Snapshot, 0, len(history)+1)
	for _, snap := range history {
		if snap.Timestamp.Before(cutoff) || !snap.Timestamp.Before(current.Timestamp) {
			continue
		}
		points = append(points, snap)
	}
	return append(points, current)
}

func queueDepth(snapshot domain.Snapshot) float64 {
	return float64(snapshot.PgBouncer.WaitingClients + snapshot.Postgres.WaitingConnections)
}

// trendAlerts runs least-squares regressions over recent history to predict
// pool exhaustion, rising wait times and a growing client queue.
func (s *Service) trendAlerts(snapshot domain.Snapshot) []domain.Alert {
	if s.state == nil {
		return nil
	}
	window := s.cfg.TrendWindow
	if window <= 0 {
		window = defaultTrendWindow
	}
	horizon := s.cfg.TrendHorizon
	if horizon <= 0 {
		horizon = defaultTrendHorizon
	}
	horizonSec := horizon.Seconds()

	points := trendPoints(s.state.History(), snapshot, window)
	if len(points) < minTrendSamples {
		return nil
	}

	xs := make([]float64, len(points))
	usage := make([]float64, len(points))
	waits := make([]float64, len(points))
	queue := make([]float64, len(points))
	for i, p := range points {
		xs[i] = p.Timestamp.Sub(snapshot.Timestamp).Seconds() // current snapshot is x=0
		usage[i] = p.Derived.PoolUsage
		waits[i] = p.Derived.WaitP95.Seconds()
		queue[i] = queueDepth(p)
	}
	samples := len(points)

	var alerts []domain.Alert

	if slope, intercept, r2, ok := linearFit(xs, usage); ok && slope > 0 && r2 >= minTrendR2 {
		if eta, ok := forecastCrossing(slope, intercept, 0, 1.0); ok && eta <= horizonSec {
			etaSeconds := int64(math.Ceil(eta))
			alerts = append(alerts, domain.Alert{
				Timestamp: snapshot.Timestamp,
				Severity:  domain.SeverityError,
				Code:      "pool_usage_trend_exhaustion",
				Message:   fmt.Sprintf("pool usage trending up %.1f%%/min; projected to reach 100%% in %ds", slope*60*100, etaSeconds),
				Details: map[string]any{
					"slope_per_sec": slope,
					"r2":            r2,
					"eta_seconds":   etaSeconds,
					"samples":       samples,
				},
			})
		}
	}

	threshold := s.cfg.WaitP95Threshold
	if threshold > 0 && snapshot.Derived.WaitP95 < threshold {
		if slope, intercept, r2, ok := linearFit(xs, waits); ok && slope > 0 && r2 >= minTrendR2 {
			if eta, ok := forecastCrossing(slope, intercept, 0, threshold.Seconds()); ok && eta <= horizonSec {
				etaSeconds := int64(math.Ceil(eta))
				alerts = append(alerts, domain.Alert{
					Timestamp: snapshot.Timestamp,
					Severity:  domain.SeverityWarn,
					Code:      "wait_time_trend_rising",
					Message:   fmt.Sprintf("p95 connection wait rising; projected to cross %s in %ds", threshold, etaSeconds),
					Details: map[string]any{
						"slope_per_sec": slope,
						"r2":            r2,
						"eta_seconds":   etaSeconds,
						"samples":       samples,
						"wait_p95":      snapshot.Derived.WaitP95.String(),
						"threshold":     threshold.String(),
					},
				})
			}
		}
	}

	if slope, intercept, r2, ok := linearFit(xs, queue); ok && slope > 0 && r2 >= minTrendR2 {
		current := queueDepth(snapshot)
		projected := slope*horizonSec + intercept
		if projected >= 1 && projected >= 2*current {
			alerts = append(alerts, domain.Alert{
				Timestamp: snapshot.Timestamp,
				Severity:  domain.SeverityWarn,
				Code:      "queue_depth_trend_rising",
				Message:   fmt.Sprintf("waiting clients trending up; projected %.0f in %s (now %.0f)", projected, horizon, current),
				Details: map[string]any{
					"slope_per_sec":   slope,
					"r2":              r2,
					"samples":         samples,
					"current_queue":   current,
					"projected_queue": projected,
					"horizon_seconds": horizonSec,
				},
			})
		}
	}

	return alerts
}
