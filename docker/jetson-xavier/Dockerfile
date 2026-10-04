# syntax=docker/dockerfile:1
#
# kokoro-run with CUDA for Jetson Xavier (NX and AGX) on JetPack 5. Build the
# ONNX Runtime base first (Dockerfile.onnxruntime, many hours, once), then this:
#
#   make image-jetson-xavier
#   docker run --runtime nvidia -p 8880:8880 kokoro-run:jetson-xavier
#
#   ORT_IMAGE    the base built by Dockerfile.onnxruntime
#   BAKE_MODELS, WITH_ESPEAK, WITH_FFMPEG, VERSION: as in ../Dockerfile.cpu

ARG ORT_IMAGE=onnxruntime-jetson-xavier:1.22.2-cuda12.2

# Built on Ubuntu 22.04, the runtime's own release: Go's Debian 12 images have a
# newer glibc than it.
FROM ubuntu:22.04 AS src
ARG GO_VERSION=1.27.1
ARG GO_SHA256=3450b45a3f9ee8568792736a5c5e70a1f2e9b36c35a8f74958c03e51d7d92bec
ENV DEBIAN_FRONTEND=noninteractive
RUN set -eux; \
	apt-get update; \
	apt-get install -y --no-install-recommends ca-certificates curl gcc libc6-dev; \
	rm -rf /var/lib/apt/lists/*; \
	curl -fsSL -o /go.tgz "https://go.dev/dl/go$GO_VERSION.linux-arm64.tar.gz"; \
	echo "$GO_SHA256  /go.tgz" | sha256sum -c -; \
	tar -C /usr/local -xzf /go.tgz; \
	rm /go.tgz
ENV PATH=/usr/local/go/bin:$PATH GOPATH=/go
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .

FROM src AS build
ARG VERSION=devel
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
	CGO_ENABLED=1 go build -trimpath -ldflags "-s -w -X github.com/androiddrew/kokoro-run/internal/cli.version=$VERSION" \
		-o /out/kokoro-run ./cmd/kokoro-run

FROM build AS models
ARG BAKE_MODELS=1
RUN set -eux; \
	mkdir /assets; \
	if [ "$BAKE_MODELS" = 1 ]; then \
		/out/kokoro-run pull --dir /assets; \
		cp /src/third_party/kokoro-82M-MODEL_CARD.md /assets/kokoro/MODEL_CARD.md; \
	fi

FROM ${ORT_IMAGE}
ARG VERSION=devel
ARG WITH_ESPEAK=0
ARG WITH_FFMPEG=1
LABEL org.opencontainers.image.title="kokoro-run" \
	org.opencontainers.image.description="OpenAI-compatible text-to-speech with Kokoro v1.0, on Jetson Xavier (JetPack 5)" \
	org.opencontainers.image.source="https://github.com/androiddrew/kokoro-run" \
	org.opencontainers.image.version="$VERSION" \
	org.opencontainers.image.licenses="Apache-2.0"
RUN set -eux; \
	packages=ca-certificates; \
	if [ "$WITH_FFMPEG" = 1 ]; then packages="$packages ffmpeg"; fi; \
	if [ "$WITH_ESPEAK" = 1 ]; then packages="$packages espeak-ng"; fi; \
	apt-get update; \
	apt-get install -y --no-install-recommends $packages; \
	rm -rf /var/lib/apt/lists/*; \
	useradd --uid 10001 --user-group --groups video --home-dir /var/lib/kokoro-run --no-create-home --shell /usr/sbin/nologin kokoro-run; \
	test "$(getent group video | cut -d: -f3)" = 44  # L4T's GPU devices (/dev/nvmap, /dev/nvhost-*) belong to gid 44

# third_party/ includes the Kokoro-82M model card, whose license and training
# data attributions go with every copy of the model. The base image keeps
# ONNX Runtime's notices in /usr/share/doc/onnxruntime, and CUDA's and cuDNN's
# licenses beside their libraries in /usr/local.
ARG DOCS=/usr/share/doc/kokoro-run
COPY LICENSE NOTICE THIRD_PARTY.md ASSETS.md $DOCS/
COPY third_party/ $DOCS/third_party/

COPY docker/jetson-xavier/config.yaml /etc/kokoro-run/config.yaml
COPY --from=build /out/kokoro-run /usr/local/bin/kokoro-run
COPY --from=models --chown=kokoro-run:kokoro-run /assets /var/lib/kokoro-run/assets

ENV KOKORO_RUN_CONFIG=/etc/kokoro-run/config.yaml
USER kokoro-run
WORKDIR /var/lib/kokoro-run
EXPOSE 8880
VOLUME /var/lib/kokoro-run/assets
HEALTHCHECK --interval=30s --timeout=10s --start-period=120s CMD ["kokoro-run", "_healthcheck"]
ENTRYPOINT ["kokoro-run"]
CMD ["serve"]
