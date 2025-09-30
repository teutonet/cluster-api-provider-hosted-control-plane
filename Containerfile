FROM scratch
ARG manager
COPY $manager /manager
USER 1000:1000
ENTRYPOINT ["/manager"]
