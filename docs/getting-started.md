# Try tearenv locally

This walkthrough builds both programs, registers one local SSH key, exposes a local HTTP server, and sends a request through the tunnel. It keeps demo state under `/tmp/tearenv-demo`.

You need the Go version declared in `go.mod`, OpenSSH client tools, Python 3, and `curl`.

## Build and grant a service

From the repository root, run:

```sh
make build
mkdir -p /tmp/tearenv-demo

./bin/tearenvd service grant \
  --users /tmp/tearenv-demo/users.json \
  --identity alice \
  --name web \
  --target 127.0.0.1:8090 \
  --local-port 18080
```

The grant creates the policy file. Registration and authentication are separate from service authorization.

## Start the target and gateway

In a second terminal, start the demo target:

```sh
python3 -m http.server 8090 --bind 127.0.0.1
```

In a third terminal, start the gateway and registration API:

```sh
./bin/tearenvd serve \
  --listen 127.0.0.1:2222 \
  --api-listen 127.0.0.1:8080 \
  --metrics-listen "" \
  --users /tmp/tearenv-demo/users.json \
  --registrations /tmp/tearenv-demo/registrations \
  --host-key /tmp/tearenv-demo/ssh_host_ed25519_key
```

The gateway creates and then reuses its host key. Its startup log includes the SHA256 fingerprint.

## Trust the host key and register

Back in the first terminal, record the public host key and compare its fingerprint with the gateway log:

```sh
ssh-keyscan -p 2222 127.0.0.1 > /tmp/tearenv-demo/known_hosts
ssh-keygen -lf /tmp/tearenv-demo/known_hosts
```

After the fingerprints match, register Alice's local key:

```sh
./bin/tearenv login \
  --api-url http://127.0.0.1:8080 \
  --identity alice \
  --server 127.0.0.1:2222 \
  --config /tmp/tearenv-demo/profile.json \
  --private-key /tmp/tearenv-demo/id_ed25519 \
  --registration /tmp/tearenv-demo/user-registration.yaml \
  --known-hosts /tmp/tearenv-demo/known_hosts
```

The API accepts the valid registration immediately and persists it under `/tmp/tearenv-demo/registrations/default/alice.yaml`. Running the same login command again is safe and returns the existing resource.

## List and connect the service

```sh
./bin/tearenv services --config /tmp/tearenv-demo/profile.json
./bin/tearenv connect --config /tmp/tearenv-demo/profile.json
```

The catalog should contain `web` on `127.0.0.1:18080`. From another terminal, send a request through the tunnel:

```sh
curl http://127.0.0.1:18080/
```

Press `Ctrl+C` in the client, gateway, and Python terminals when finished. For a real deployment, keep the registration store and SSH host key on persistent protected storage, expose the API through TLS, and decide which network is allowed to claim identities.
