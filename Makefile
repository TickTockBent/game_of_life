.PHONY: build run test docker-build docker-push docker-run clean setup-auth deploy registry-auth

DOCKER_REGISTRY ?= 192.168.68.100:5000
VERSION ?= latest

build:
	go build -o bin/engine ./cmd/engine
	go build -o bin/controller ./cmd/controller
	go build -o bin/router ./cmd/router
	go build -o bin/web ./cmd/web
	go build -o bin/public-engine ./cmd/public-engine

run: build
	./bin/engine

test:
	go test ./...

docker-build:
	@echo "Building all components..."
	docker build -f Dockerfile.engine -t $(DOCKER_REGISTRY)/gameoflife-engine:$(VERSION) .
	docker build -f Dockerfile.controller -t $(DOCKER_REGISTRY)/gameoflife-controller:$(VERSION) .
	docker build -f Dockerfile.router -t $(DOCKER_REGISTRY)/gameoflife-router:$(VERSION) .
	docker build -f Dockerfile.web -t $(DOCKER_REGISTRY)/gameoflife-web:$(VERSION) .

# Build public engine for external distribution
docker-build-public:
	@echo "Building public engine..."
	docker build -f Dockerfile.public-engine -t gameoflife-public-engine:$(VERSION) .

docker-run: docker-build
	docker run -p 8080:8080 -e NODE_NAME=test-node $(DOCKER_REGISTRY)/gameoflife-engine:$(VERSION)

clean:
	rm -rf bin/

docker-push: docker-build
	@echo "Pushing images to $(DOCKER_REGISTRY)..."
	docker push $(DOCKER_REGISTRY)/gameoflife-engine:$(VERSION)
	docker push $(DOCKER_REGISTRY)/gameoflife-controller:$(VERSION)
	docker push $(DOCKER_REGISTRY)/gameoflife-router:$(VERSION)
	docker push $(DOCKER_REGISTRY)/gameoflife-web:$(VERSION)

docker-multiarch:
	./scripts/build-multiarch.sh

docker-fast:
	./scripts/fast-build.sh

setup-auth:
	./scripts/setup-registry-auth.sh

registry-auth:
	@export VAULT_ADDR='http://192.168.68.100:8200' && ./scripts/setup-registry-auth.sh

deploy: docker-push
	kubectl apply -f manifests/

deploy-fresh: registry-auth deploy
	@echo "Fresh deployment with registry auth completed"

local-test:
	@echo "Starting local test server..."
	PORT=8080 NODE_NAME=local go run cmd/engine/main.go