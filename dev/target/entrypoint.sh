#!/bin/sh
set -e

# Start the inner Docker daemon (the container runs --privileged).
dockerd >/var/log/dockerd.log 2>&1 &

# Wait for the Docker socket.
i=0
while [ ! -S /var/run/docker.sock ] && [ "$i" -lt 60 ]; do
    i=$((i + 1))
    sleep 1
done

# Ensure the injected public key is usable, then run sshd in the foreground.
mkdir -p /root/.ssh
chmod 700 /root/.ssh
if [ -f /root/.ssh/authorized_keys ]; then
    chmod 600 /root/.ssh/authorized_keys
fi

exec /usr/sbin/sshd -D -e
