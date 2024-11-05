# BrickStor CSI Driver makefile
#
# Test options to be set before run tests:
# NOCOLOR=1                # disable colors
# - TEST_K8S_IP=10.3.199.250 # e2e k8s tests
#

DRIVER_NAME = brickstor-csi-driver
IMAGE_NAME ?= ${DRIVER_NAME}
VERSION = 0.0.1

BASE_IMAGE ?= alpine:3.20
BUILD_IMAGE ?= golang:1.22
CSI_SANITY_VERSION_TAG ?= v4.0.0

DOCKER_FILE = Dockerfile
DOCKER_FILE_TESTS = Dockerfile.tests
DOCKER_FILE_TEST_CSI_SANITY = Dockerfile.csi-sanity

REGISTRY ?= racktop
REGISTRY_LOCAL ?= 10.2.21.92:5000

GIT_BRANCH = $(shell git rev-parse --abbrev-ref HEAD | sed -e "s/.*\\///")
GIT_TAG = $(shell git describe --tags)
COMMIT ?= $(shell git rev-parse --short HEAD)
DATETIME ?= $(shell date +'%F_%T')
LDFLAGS ?= \
	-X github.com/racktopsystems/brickstor-csi-driver/pkg/driver.Version=${VERSION} \
	-X github.com/racktopsystems/brickstor-csi-driver/pkg/driver.Commit=${COMMIT} \
	-X github.com/racktopsystems/brickstor-csi-driver/pkg/driver.DateTime=${DATETIME}

DOCKER_ARGS = --build-arg BUILD_IMAGE=${BUILD_IMAGE} \
              --build-arg BASE_IMAGE=${BASE_IMAGE}

# Pushing Docker image(s) to registry on demand
PUSH=
DOCKER_PUSH_ARG = $(if $(PUSH),--push,)

# Tag docker images as latest on demand
LATEST=
DOCKER_TAG_LATEST_ARG = $(if $(LATEST),-t ${REGISTRY}/${IMAGE_NAME}:latest,)

DOCKER_TAG_ARGS = ${DOCKER_TAG_LATEST_ARG} \
	-t ${REGISTRY}/${IMAGE_NAME}:${VERSION} \
	-t ${REGISTRY}/${IMAGE_NAME}:${COMMIT} \ 

.PHONY: all
all:
	@echo "Some commands:"
	@echo "  build                          - build driver container"
	@echo "  test-csi-sanity-container      - run csi-sanity test suite"
	@echo "  container-push-local           - build driver container (PUSH to REGISTRY_LOCAL)"
	@echo "  test-all-local-image-container - run all test using driver from REGISTRY_LOCAL)"
	@echo ""
	@echo "Variables:"
	@echo "  VERSION:        ${VERSION}"
	@echo "  GIT_BRANCH:     ${GIT_BRANCH}"
	@echo "  GIT_TAG:        ${GIT_TAG}"
	@echo "  COMMIT:         ${COMMIT}"
	@echo "  REGISTRY_LOCAL: ${REGISTRY_LOCAL}"
	@echo "Testing variables:"
	@echo "  TEST_K8S_IP: ${TEST_K8S_IP}"

.PHONY: vet
vet:
	CGO_ENABLED=0 GOTOOLCHAIN=local go vet -v ./...

.PHONY: fmt
fmt:
	CGO_ENABLED=0 GOTOOLCHAIN=local go fmt ./...

DRIVER_LINUX_AMD64_BIN = bin/${DRIVER_NAME}_linux_amd64
DRIVER_LINUX_ARM64_BIN = bin/${DRIVER_NAME}_linux_arm64

DRIVER_BINS = \
	${DRIVER_LINUX_AMD64_BIN} \
	${DRIVER_LINUX_ARM64_BIN}

AMD64_BINS = \
	${DRIVER_LINUX_AMD64_BIN}

ARM64_BINS = \
	${DRIVER_LINUX_ARM64_BIN}

$(AMD64_BINS): GOARCH=amd64
$(ARM64_BINS): GOARCH=arm64

.PHONY: build
build: $(DRIVER_BINS)

$(DRIVER_BINS): vet fmt
	@CGO_ENABLED=0 GOTOOLCHAIN=local GOOS=linux GOARCH=$(GOARCH) \
		go build -ldflags "${LDFLAGS}" -o $@ ./cmd

.PHONY: container-build
container-build:
	@echo [INFO] Building docker image ${REGISTRY}/${IMAGE_NAME}:${VERSION}
	GOTOOLCHAIN=local docker build \
		-f ${DOCKER_FILE} \
		--build-arg VERSION=${VERSION} ${DOCKER_ARGS} \
		${DOCKER_TAG_ARGS} ${DOCKER_PUSH_ARG} .

.PHONY: container-push-remote
container-push-remote:
	docker buildx use mybuilder 2>/dev/null || docker buildx create --name mybuilder --use
	docker buildx build \
		--progress=plain \
		--platform linux/amd64,linux/arm64 \
		--build-arg VERSION=${VERSION} ${DOCKER_ARGS} \
		--file ${DOCKER_FILE} \
		--push \
		${DOCKER_TAG_ARGS} .

# run e2e k8s tests using image from local docker registry
.PHONY: test-e2e-k8s-local-image
test-e2e-k8s-local-image: check-env-TEST_K8S_IP
	sed -e "s/image: racktopsystems/image: ${REGISTRY_LOCAL}/g" \
		./deploy/kubernetes/brickstor-csi-driver.yaml > /tmp/brickstor-csi-driver-local.yaml
	go test -timeout 30m tests/e2e/driver_test.go -v -count 1 \
		--k8sConnectionString="root@${TEST_K8S_IP}" \
		--k8sDeploymentFile="/tmp/brickstor-csi-driver-local.yaml" \
		--k8sSecretFile="./_configs/driver-config-single-default.yaml" \
		--fsTypeFlag="nfs"
	go test -timeout 30m tests/e2e/driver_test.go -v -count 1 \
		--k8sConnectionString="root@${TEST_K8S_IP}" \
		--k8sDeploymentFile="/tmp/brickstor-csi-driver-local.yaml" \
		--k8sSecretFile="./_configs/driver-config-single-cifs.yaml"

.PHONY: test-e2e-k8s-local-image-container
test-e2e-k8s-local-image-container: check-env-TEST_K8S_IP
	docker build -f ${DOCKER_FILE_TESTS} -t ${IMAGE_NAME}-test --build-arg VERSION=${VERSION} \
	--build-arg TESTRAIL_URL=${TESTRAIL_URL} \
	--build-arg TESTRAIL_USR=${TESTRAIL_USR} \
	--build-arg TESTRAIL_PSWD=${TESTRAIL_PSWD} ${DOCKER_ARGS} .
	docker run -i --rm -v ${HOME}/.ssh:/root/.ssh:ro \
		-e NOCOLORS=${NOCOLORS} -e TEST_K8S_IP=${TEST_K8S_IP} \
		${IMAGE_NAME}-test test-e2e-k8s-local-image

# run e2e k8s tests using image from hub.docker.com
.PHONY: test-e2e-k8s-remote-image
test-e2e-k8s-remote-image: check-env-TEST_K8S_IP
	go test -timeout 30m tests/e2e/driver_test.go -v -count 1 \
		--k8sConnectionString="root@${TEST_K8S_IP}" \
		--k8sDeploymentFile="../../deploy/kubernetes/brickstor-csi-driver.yaml" \
		--k8sSecretFile="./_configs/driver-config-single-default.yaml" \
		--fsTypeFlag="nfs"
	go test -timeout 30m tests/e2e/driver_test.go -v -count 1 \
		--k8sConnectionString="root@${TEST_K8S_IP}" \
		--k8sDeploymentFile="../../deploy/kubernetes/brickstor-csi-driver.yaml" \
		--k8sSecretFile="./_configs/driver-config-single-cifs.yaml"

.PHONY: test-e2e-k8s-local-image-container
test-e2e-k8s-remote-image-container: check-env-TEST_K8S_IP
	docker build -f ${DOCKER_FILE_TESTS} -t ${IMAGE_NAME}-test --build-arg VERSION=${VERSION} \
	--build-arg TESTRAIL_URL=${TESTRAIL_URL} \
	--build-arg TESTRAIL_USR=${TESTRAIL_USR} \
	--build-arg TESTRAIL_PSWD=${TESTRAIL_PSWD} ${DOCKER_ARGS} .
	docker run -i --rm -v ${HOME}/.ssh:/root/.ssh:ro \
		-e NOCOLORS=${NOCOLORS} -e TEST_K8S_IP=${TEST_K8S_IP} \
		${IMAGE_NAME}-test test-e2e-k8s-remote-image

# csi-sanity tests:
# - tests make requests to actual BrickStor, config file: ./tests/csi-sanity/*.yaml
# - create container with driver and csi-sanity (https://github.com/kubernetes-csi/csi-test)
# - run container to execute tests
# - nfs client requires running container as privileged one
.PHONY: test-csi-sanity-container
test-csi-sanity-container:
	docker build \
		--build-arg CSI_SANITY_VERSION_TAG=${CSI_SANITY_VERSION_TAG} \
		-f ${DOCKER_FILE_TEST_CSI_SANITY} \
		-t ${IMAGE_NAME}-test-csi-sanity ${DOCKER_ARGS} .
	docker run --privileged=true -i -e NOCOLORS=${NOCOLORS} ${IMAGE_NAME}-test-csi-sanity
	docker image prune -f
	docker images | grep brickstor-csi-driver-test-csi-sanity | awk '{print $$1}' | xargs docker rmi -f

# run all tests (local registry image)
.PHONY: test-all-local-image
test-all-local-image: \
	test-e2e-k8s-local-image
.PHONY: test-all-local-image-container
test-all-local-image-container: \
	test-csi-sanity-container \
	test-e2e-k8s-local-image-container

# run all tests (hub.github.com image)
.PHONY: test-all-remote-image
test-all-remote-image: \
	test-e2e-k8s-remote-image
.PHONY: test-all-remote-image-container
test-all-remote-image-container: \
	test-csi-sanity-container \
	test-e2e-k8s-remote-image-container

.PHONY: check-env-TEST_K8S_IP
check-env-TEST_K8S_IP:
ifeq ($(strip ${TEST_K8S_IP}),)
	$(error "Error: environment variable TEST_K8S_IP is not set (i.e 10.3.199.250)")
endif

.PHONY: release
release: git-tag container-push-remote

.PHONY: git-tag
git-tag:
	@echo "New tag: 'v${VERSION}'\n\n \
	To change version: 'make release VERSION=X.X.X'.\n\n \
	To add latest tag: 'make release LATEST=1'.\n\n \
	Confirm that:\n \
		1. New version will be based on current '${GIT_BRANCH}' git branch\n \
		2. Driver container '${IMAGE_NAME}' will be built\n \
		3. Login to hub.docker.com will be requested\n \
		4. CHANGELOG.md file will be updated\n \
		5. Git tag 'v${VERSION}' will be created and pushed to the repository.\n\n \
		6. Driver version '${REGISTRY}/${IMAGE_NAME}:${VERSION}' will be pushed to hub.docker.com\n \
		Are you sure? [y/N]: "
	@(read ANSWER && case "$$ANSWER" in [yY]) true;; *) false;; esac)
	git checkout -b ${VERSION}
	sed -i '' 's/:latest/:${VERSION}/g' deploy/kubernetes/brickstor-csi-driver.yaml
	docker login
	git add deploy/kubernetes/brickstor-csi-driver.yaml
	git commit -m "release v${VERSION}"
	git push origin ${VERSION}
	git tag v${VERSION}
	git push --tags

.PHONY: clean
clean:
	-go clean -r -x
	-rm -rf bin