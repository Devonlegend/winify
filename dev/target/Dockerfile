# A throwaway Linux "target server" for local end-to-end testing.
# It runs a real Docker daemon (privileged) plus sshd, so the control-center can
# SSH in and run the exact same deploy pipeline it would run in production.
FROM docker:dind

# The dind image already ships git, docker, docker compose and wget; we only
# need an SSH server and curl (used by the deploy health check).
RUN apk add --no-cache openssh curl \
    && ssh-keygen -A

# The dind image points the CLI at a compose service name; use the local socket.
ENV DOCKER_HOST=unix:///var/run/docker.sock

COPY entrypoint.sh /usr/local/bin/cc-entrypoint.sh
RUN sed -i 's/\r$//' /usr/local/bin/cc-entrypoint.sh \
    && chmod +x /usr/local/bin/cc-entrypoint.sh

EXPOSE 22
ENTRYPOINT ["/usr/local/bin/cc-entrypoint.sh"]
