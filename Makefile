IMG ?= backup-restore-operator:dev
CONTROLLER_GEN ?= go run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.17.3
GOLANGCI_LINT ?= go run github.com/golangci/golangci-lint/cmd/golangci-lint@v1.64.8
ENVTEST_K8S_VERSION ?= 1.32.0

.PHONY: all generate manifests fmt vet lint test test-unit test-integration build docker-build docker-push install uninstall deploy undeploy helm-lint helm-package e2e

all: fmt vet test build

generate:
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./api/v1alpha1"

manifests:
	$(CONTROLLER_GEN) crd webhook paths="./api/v1alpha1" output:crd:artifacts:config=config/crd/bases output:webhook:artifacts:config=config/webhook
	$(CONTROLLER_GEN) rbac:roleName=backup-restore-operator paths="./internal/controller" output:rbac:artifacts:config=config/rbac

fmt:
	go fmt ./...

vet:
	go vet ./...

lint:
	$(GOLANGCI_LINT) run ./...

test: test-unit test-integration

test-unit:
	go test ./api/... ./cmd/... ./internal/... -coverprofile cover.out

test-integration:
	powershell -NoProfile -ExecutionPolicy Bypass -File hack/test-integration.ps1 -KubernetesVersion $(ENVTEST_K8S_VERSION)

build: generate manifests
	go build -o bin/manager.exe ./cmd/manager

docker-build:
	docker build -t $(IMG) .

docker-push:
	docker push $(IMG)

install: manifests
	kubectl apply -f config/crd/bases

uninstall:
	kubectl delete -f config/crd/bases --ignore-not-found=true

deploy: manifests
	kubectl apply -k config/default

undeploy:
	kubectl delete -k config/default --ignore-not-found=true

helm-lint: manifests
	helm lint charts/backup-restore-operator

helm-package: helm-lint
	helm package charts/backup-restore-operator -d dist

e2e:
	powershell -NoProfile -ExecutionPolicy Bypass -File test/e2e/run.ps1
