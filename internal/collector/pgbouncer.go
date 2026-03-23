package collector

import (
	"context"
	"time"

	"poolwatch/internal/domain"
)

func (s *Service) collectPgBouncer(ctx context.Context) (domain.PgBouncerMetrics, error) {
	result := domain.PgBouncerMetrics{}

	poolRows, err := s.pgb.QueryContext(ctx, `show pools`)
	if err != nil {
		return result, err
	}
	defer poolRows.Close()

	for poolRows.Next() {
		var database string
		var user string
		var clActive, clWaiting, clActiveCancelReq, clWaitingCancelReq int
		var svActive, svActiveCancel, svBeingCanceled, svIdle, svUsed, svTested, svLogin int
		var maxwait float64
		var maxwaitUs int64
		var poolMode string
		if err := poolRows.Scan(
			&database,
			&user,
			&clActive,
			&clWaiting,
			&clActiveCancelReq,
			&clWaitingCancelReq,
			&svActive,
			&svActiveCancel,
			&svBeingCanceled,
			&svIdle,
			&svUsed,
			&svTested,
			&svLogin,
			&maxwait,
			&maxwaitUs,
			&poolMode,
		); err != nil {
			return result, err
		}
		result.ActiveClients += clActive
		result.WaitingClients += clWaiting
		result.ActiveServers += svActive
		result.IdleServers += svIdle
		wait := time.Duration(maxwaitUs) * time.Microsecond
		if wait == 0 && maxwait > 0 {
			wait = time.Duration(maxwait * float64(time.Second))
		}
		if wait > 0 {
			result.WaitSamples = append(result.WaitSamples, wait)
		}
		if wait > result.MaxWait {
			result.MaxWait = wait
		}
		result.Databases = append(result.Databases, domain.PoolDatabaseStats{
			Database:       database,
			User:           user,
			ClActive:       clActive,
			ClWaiting:      clWaiting,
			SvActive:       svActive,
			SvIdle:         svIdle,
			PoolMode:       poolMode,
			MaxwaitSeconds: int64(wait / time.Second),
		})
	}
	if err := poolRows.Err(); err != nil {
		return result, err
	}

	statsRows, err := s.pgb.QueryContext(ctx, `show stats`)
	if err != nil {
		return result, nil
	}
	defer statsRows.Close()

	var waitTotal time.Duration
	var waitCount int
	for statsRows.Next() {
		var database string
		var totalXactCount, totalQueryCount, totalReceived, totalSent, totalXactTime, totalQueryTime int64
		var totalWaitTime int64
		var avgXactCount, avgQueryCount, avgRecv, avgSent, avgXactTime, avgQueryTime float64
		var avgWaitTime float64
		if err := statsRows.Scan(
			&database,
			&totalXactCount,
			&totalQueryCount,
			&totalReceived,
			&totalSent,
			&totalXactTime,
			&totalQueryTime,
			&totalWaitTime,
			&avgXactCount,
			&avgQueryCount,
			&avgRecv,
			&avgSent,
			&avgXactTime,
			&avgQueryTime,
			&avgWaitTime,
		); err != nil {
			return result, nil
		}
		_ = database
		result.ConnectionChurn += totalXactCount
		if totalWaitTime > 0 {
			waitTotal += time.Duration(totalWaitTime) * time.Microsecond
			waitCount++
		}
		if avgWaitTime > 0 {
			result.WaitSamples = append(result.WaitSamples, time.Duration(avgWaitTime*1000)*time.Microsecond)
		}
	}
	if waitCount > 0 {
		result.AvgWait = waitTotal / time.Duration(waitCount)
	}

	return result, nil
}
