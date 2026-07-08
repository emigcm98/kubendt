# Slim capture image for KubeNDT packet capture.
#
# By default KubeNDT uses nicolaka/netshoot (pullable from Docker Hub, no build
# needed). Build and use this image instead when you want a smaller footprint
# or an air-gapped/offline registry:
#
#   docker build -t kubendt/capture:latest deploy/custom_images/capture
#   # then point the backend at it:
#   export KUBENDT_CAPTURE_IMAGE=kubendt/capture:latest
#
# The backend execs tshark into a container built from this image; the image
# only needs tshark/dumpcap on PATH and a /bin/sh.
FROM alpine:3.20

RUN apk add --no-cache tshark

# tshark/dumpcap live in /usr/bin; nothing else to configure. The container is
# kept alive by the backend with `sleep`, and tshark is exec'd into it.
