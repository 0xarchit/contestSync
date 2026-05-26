FROM ghcr.io/0xarchit/contestsync_server:latest AS source
FROM scratch
COPY --from=source /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=source /server /server
EXPOSE 7860
ENV PORT=7860
ENV ENV=production
USER 1000:1000
ENTRYPOINT ["/server"]
