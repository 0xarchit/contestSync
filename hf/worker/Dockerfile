FROM ghcr.io/0xarchit/contestsync_worker:latest AS source
FROM scratch
COPY --from=source /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=source /worker /worker
EXPOSE 7860
ENV PORT=7860
ENV ENV=production
USER 1000:1000
ENTRYPOINT ["/worker"]
