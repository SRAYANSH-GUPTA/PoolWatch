BINARY  ?= poolwatch
PKG     ?= ./cmd/poolwatch
IMAGE   ?= poolwatch:local
COMPOSE ?= docker compose

.PHONY: build run test vet lint docker-build up down load clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o $(BINARY) $(PKG)

run:
	go run $(PKG)

test:
	go test -race ./...

vet:
	go vet ./...

lint:
	@out="$$(gofmt -l .)"; \
	if [ -n "$$out" ]; then echo "gofmt needed on:"; echo "$$out"; exit 1; fi

docker-build:
	docker build -t $(IMAGE) .

up:
	$(COMPOSE) up -d --build

down:
	$(COMPOSE) --profile load down

load:
	$(COMPOSE) --profile load up --build

clean:
	rm -f $(BINARY) coverage.out
	rm -rf dist/
