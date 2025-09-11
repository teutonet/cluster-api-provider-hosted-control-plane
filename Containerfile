ARG manager
FROM cgr.dev/chainguard/static:latest-glibc
COPY $manager /manager
USER 1000:1000
ENTRYPOINT ["/manager"]
