# The KubeNDT P4 switch node: BMv2's simple_switch built for forwarding.
# The official p4lang/behavioral-model image is a debug build (debugger on,
# logging macros and event logger on, no optimization) with no versioned
# tag, and it moves packets at a thirtieth of this one. It serves here only
# as the build environment, for the Thrift and nanomsg builds bmv2 wants.
#
# Published as ghcr.io/emigcm98/kubendt-p4:<bmv2 version> and :latest by
# p4-image.yml on every change to this folder on main. The tag is bmv2's
# version, not KubeNDT's: the backend only talks simple_switch_CLI to it.
FROM docker.io/p4lang/behavioral-model@sha256:faa2138c278939aa15572036e30db444801d6854114734521864deb8e6e0fadc AS build

# The version in the tag, a release tag of p4lang/behavioral-model.
ARG BMV2_VERSION=1.15.8
ADD https://github.com/p4lang/behavioral-model/archive/refs/tags/${BMV2_VERSION}.tar.gz /bmv2.tar.gz

# After the install, /out keeps only what simple_switch and its CLI need: the
# binaries, the libraries from /usr/local they link, the CLI's Python modules
# out of the official image's venv (the rest of it, scapy, ptf, grpc and the
# like, is not ours to ship), and the licenses of what is copied.
RUN tar xzf /bmv2.tar.gz -C / && cd "/behavioral-model-${BMV2_VERSION}" \
 && cmake -S . -B build -DCMAKE_BUILD_TYPE=Release \
      -DENABLE_LOGGING_MACROS=OFF -DENABLE_ELOGGER=OFF -DENABLE_DEBUGGER=OFF \
      -DWITH_PI=OFF -DWITH_PDFIXED=OFF -DWITH_THRIFT=ON -DWITH_NANOMSG=ON \
      -DWITH_STRESS_TESTS=OFF -DENABLE_CCACHE=OFF \
 && cmake --build build -j"$(nproc)" \
 && cmake --install build \
 && mkdir -p /out/usr/local/bin /out/usr/local/lib \
 && cp /usr/local/bin/simple_switch /usr/local/bin/simple_switch_CLI /out/usr/local/bin/ \
 && for l in $(ldd /usr/local/bin/simple_switch | awk '/=> \/usr\/local/{print $3}'); do cp -L "$l" /out/usr/local/lib/; done \
 && sp=/root/p4-python-venv/lib/python3.12/site-packages && mkdir -p /out/py \
 && cp -r "$sp/bm_runtime" "$sp/sswitch_runtime" "$sp/thrift" /out/py/ \
 && cp "$sp/runtime_CLI.py" "$sp/sswitch_CLI.py" "$sp/bmpy_utils.py" /out/py/ \
 && sed -i '1s|^#!.*python.*|#!/usr/bin/python3|' /out/usr/local/bin/simple_switch_CLI \
 && mkdir -p /out/doc/bmv2 /out/doc/thrift /out/doc/nanomsg \
 && cp LICENSE /out/doc/bmv2/ \
 && curl -fsSL -o /out/doc/thrift/LICENSE https://raw.githubusercontent.com/apache/thrift/v0.13.0/LICENSE \
 && curl -fsSL -o /out/doc/thrift/NOTICE https://raw.githubusercontent.com/apache/thrift/v0.13.0/NOTICE \
 && curl -fsSL -o /out/doc/nanomsg/COPYING https://raw.githubusercontent.com/nanomsg/nanomsg/1.0.0/COPYING

FROM docker.io/library/ubuntu:24.04
LABEL org.opencontainers.image.source="https://github.com/emigcm98/kubendt" \
      org.opencontainers.image.description="BMv2 simple_switch built for forwarding, the KubeNDT P4 switch node" \
      org.opencontainers.image.licenses="Apache-2.0"
RUN apt-get update -qq \
 && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends \
      libboost-program-options1.83.0 libboost-filesystem1.83.0 libboost-thread1.83.0 libboost-system1.83.0 \
      libgmp10 libpcap0.8t64 libxxhash0 libjsoncpp25 python3 python3-six iproute2 ethtool tini \
 && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/usr/local /usr/local
COPY --from=build /out/py /usr/local/lib/python3.12/dist-packages
COPY --from=build /out/doc /usr/share/doc
RUN ldconfig && simple_switch --version && python3 -c "import thrift, bm_runtime, sswitch_runtime"

COPY entrypoint.sh /usr/local/bin/entrypoint.sh
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/entrypoint.sh"]
