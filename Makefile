# Build, test and package kokoro-run. Native tests need ONNX Runtime and the
# Kokoro files: set KOKORO_TEST_ORT and KOKORO_TEST_ASSETS (see README).

IMAGE ?= kokoro-run
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo devel)
BAKE_MODELS ?= 1
WITH_ESPEAK ?= 0
WITH_FFMPEG ?= 1
IMAGE_PLATFORMS ?= linux/amd64 linux/arm64
HOST_ARCH := $(if $(filter aarch64 arm64,$(shell uname -m)),arm64,amd64)
BUILD_ARGS = --build-arg VERSION=$(VERSION) --build-arg BAKE_MODELS=$(BAKE_MODELS) \
	--build-arg WITH_ESPEAK=$(WITH_ESPEAK) --build-arg WITH_FFMPEG=$(WITH_FFMPEG)

.PHONY: build test test-native vet image-cpu image-cuda image-jetson-orin-onnxruntime image-jetson-orin \
	image-jetson-xavier-onnxruntime image-jetson-xavier

build:
	CGO_ENABLED=1 go build -ldflags "-X github.com/androiddrew/kokoro-run/internal/cli.version=$(VERSION)" \
		-o bin/kokoro-run ./cmd/kokoro-run

vet:
	go vet ./...

# Native tests skip unless KOKORO_TEST_ORT and KOKORO_TEST_ASSETS are set.
test:
	go test -race ./...

test-native:
	@test -n "$(KOKORO_TEST_ORT)" -a -n "$(KOKORO_TEST_ASSETS)" || { echo "set KOKORO_TEST_ORT and KOKORO_TEST_ASSETS" >&2; exit 1; }
	go test -race -count=1 -v ./integration

# The CPU image, built and loaded per platform as $(IMAGE):cpu-<arch>, since
# the classic image store can't hold a multi-platform image; the host's
# platform is also tagged $(IMAGE):cpu. Other platforms build under QEMU.
image-cpu:
	set -e; for p in $(IMAGE_PLATFORMS); do \
		docker buildx build --platform $$p -f docker/Dockerfile.cpu $(BUILD_ARGS) \
			--load -t $(IMAGE):cpu-$${p#linux/} . ; \
	done
	$(if $(filter linux/$(HOST_ARCH),$(IMAGE_PLATFORMS)),docker tag $(IMAGE):cpu-$(HOST_ARCH) $(IMAGE):cpu)

image-cuda:
	docker buildx build --platform linux/amd64 -f docker/Dockerfile.cuda $(BUILD_ARGS) --load -t $(IMAGE):cuda .

# Jetson Orin on JetPack 7.2, built on the Orin. The ONNX Runtime base takes
# hours and is built once; JETSON_PARALLEL compile jobs need 5-10 GB each.
JETSON_ORT_VERSION ?= 1.23.0
JETSON_PARALLEL ?= 3
JETSON_ORIN_ORT_IMAGE ?= onnxruntime-jetson-orin:$(JETSON_ORT_VERSION)-cuda13.2

image-jetson-orin-onnxruntime:
	docker buildx build --platform linux/arm64 -f docker/jetson-orin/Dockerfile.onnxruntime \
		--build-arg ORT_VERSION=$(JETSON_ORT_VERSION) --build-arg PARALLEL=$(JETSON_PARALLEL) \
		--load -t $(JETSON_ORIN_ORT_IMAGE) docker/jetson-orin

image-jetson-orin:
	docker buildx build --platform linux/arm64 -f docker/jetson-orin/Dockerfile $(BUILD_ARGS) \
		--build-arg ORT_IMAGE=$(JETSON_ORIN_ORT_IMAGE) --load -t $(IMAGE):jetson-orin .

# Jetson Xavier (NX and AGX) on JetPack 5, built on the Xavier: CUDA 12.2 through
# cuda-compat, cuDNN 9.3 and ONNX Runtime 1.22 for sm_72. The base takes many
# hours; XAVIER_PARALLEL compile jobs need 3-6 GB each.
XAVIER_ORT_VERSION ?= 1.22.2
XAVIER_PARALLEL ?= 2
JETSON_XAVIER_ORT_IMAGE ?= onnxruntime-jetson-xavier:$(XAVIER_ORT_VERSION)-cuda12.2

image-jetson-xavier-onnxruntime:
	docker buildx build --platform linux/arm64 -f docker/jetson-xavier/Dockerfile.onnxruntime \
		--build-arg ORT_VERSION=$(XAVIER_ORT_VERSION) --build-arg PARALLEL=$(XAVIER_PARALLEL) \
		--load -t $(JETSON_XAVIER_ORT_IMAGE) docker/jetson-xavier

image-jetson-xavier:
	docker buildx build --platform linux/arm64 -f docker/jetson-xavier/Dockerfile $(BUILD_ARGS) \
		--build-arg ORT_IMAGE=$(JETSON_XAVIER_ORT_IMAGE) --load -t $(IMAGE):jetson-xavier .
