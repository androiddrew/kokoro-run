# syntax=docker/dockerfile:1
#
# kokoro-run with CUDA for Jetson Orin on JetPack 7.2. Build the ONNX Runtime
# base image first (Dockerfile.onnxruntime, hours, once), then this (minutes):
#
#   make image-jetson-orin
#   docker run --runtime nvidia -p 8880:8880 kokoro-run:jetson-orin
#
#   ORT_IMAGE    the base built by Dockerfile.onnxruntime
#   BAKE_MODELS, WITH_ESPEAK, WITH_FFMPEG, VERSION: as in ../Dockerfile.cpu

ARG ORT_IMAGE=onnxruntime-jetson-orin:1.23.0-cuda13.2
ARG GO_IMAGE=golang:1.27-bookworm

# Built natively on the Orin. Debian 12's glibc is older than Ubuntu 24.04's.
FROM ${GO_IMAGE} AS src
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
	org.opencontainers.image.description="OpenAI-compatible text-to-speech with Kokoro v1.0, on Jetson Orin (JetPack 7.2)" \
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
	useradd --uid 10001 --user-group --home-dir /var/lib/kokoro-run --no-create-home --shell /usr/sbin/nologin kokoro-run

# third_party/ includes the Kokoro-82M model card, whose license and training
# data attributions go with every copy of the model. The base image keeps
# ONNX Runtime's notices in /usr/share/doc/onnxruntime.
ARG DOCS=/usr/share/doc/kokoro-run
COPY LICENSE NOTICE THIRD_PARTY.md ASSETS.md $DOCS/
COPY third_party/ $DOCS/third_party/

COPY docker/jetson-orin/config.yaml /etc/kokoro-run/config.yaml
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
