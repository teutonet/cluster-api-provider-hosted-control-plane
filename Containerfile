FROM cgr.dev/chainguard/static:latest-glibc
ARG manager
COPY $manager /manager
USER 1000:1000
ENTRYPOINT ["/manager"]
