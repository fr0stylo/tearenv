# Try tearenv locally

This walkthrough builds both programs, exposes a local HTTP server through `tearenvd`, enrolls one identity, and sends a request through the tunnel. It keeps all demo state under `/tmp/tearenv-demo`.

You need the Go version declared in `go.mod`, OpenSSH client tools, Python 3, and `curl`.

## Build both programs

From the repository root, run:

```sh
make build
mkdir -p /tmp/tearenv-demo
```

The build creates `bin/tearenv` and `bin/tearenvd`.

## Create an identity and grant a service

Create the one-time invite before starting the gateway. `tearenvd serve` expects the credential file to exist and contain at least one registered identity or pending invite.

```sh
INVITE=$(./bin/tearenvd invite \
  -users /tmp/tearenv-demo/users.json \
  -identity alice)

./bin/tearenvd service grant \
  -users /tmp/tearenv-demo/users.json \
  -identity alice \
  -name web \
  -target 127.0.0.1:8080 \
  -local-port 18080
```

The invite is printed only once. Keep this terminal open for the login step.

## Start the target and gateway

In a second terminal, start the demo target:

```sh
python3 -m http.server 8080 --bind 127.0.0.1
```

In a third terminal, start the gateway:

```sh
./bin/tearenvd serve \
  -listen 127.0.0.1:2222 \
  -users /tmp/tearenv-demo/users.json \
  -host-key /tmp/tearenv-demo/ssh_host_ed25519_key
```

The gateway creates and then reuses the host key. Its startup log includes the SHA256 fingerprint.

## Trust the demo host key and log in

Back in the first terminal, record the public host key:

```sh
ssh-keyscan -p 2222 127.0.0.1 > /tmp/tearenv-demo/known_hosts
```

Compare its fingerprint with the fingerprint from the `tearenvd` startup log:

```sh
ssh-keygen -lf /tmp/tearenv-demo/known_hosts
```

After the fingerprints match, redeem the invite:

```sh
./bin/tearenv login \
  -config /tmp/tearenv-demo/profile.json \
  -known-hosts /tmp/tearenv-demo/known_hosts \
  -server 127.0.0.1:2222 \
  -identity alice \
  -invite "$INVITE"
```

The invite is now consumed. The client profile contains a new personal token and is saved with mode `0600`.

## List and connect the granted service

Check the catalog:

```sh
./bin/tearenv services -config /tmp/tearenv-demo/profile.json
```

The output should be:

```text
web	127.0.0.1:18080
```

Start the local listener and leave it running:

```sh
./bin/tearenv connect -config /tmp/tearenv-demo/profile.json
```

From another terminal, send a request through the tunnel:

```sh
curl http://127.0.0.1:18080/
```

Press `Ctrl+C` in the `tearenv connect`, `tearenvd`, and Python terminals when you're done. The demo files remain under `/tmp/tearenv-demo` for inspection and can be removed when you no longer need them.

## Move from the demo to a deployment

For a real environment, keep the gateway host key stable, store the credential file on protected writable storage, publish the SSH listener, and give each developer an independently generated invite. Use [the operator guide](operator-guide.md) for static and Kubernetes-backed grants, then send developers [the developer guide](developer-guide.md).
