# OIDC overlay

This overlay connects an existing OIDC provider; it doesn't deploy Dex. Replace
the example issuer, client ID, audience, subject-token type, and scopes in
`deployment-patch.yaml`. ID tokens are the compatible default for Dex and
Keycloak. Create the SSH user CA Secret before applying the overlay:

```sh
ssh-keygen -t ed25519 -N '' -C tearenv-user-ca -f ./tearenv_user_ca
kubectl create namespace tearenv-system --dry-run=client -o yaml | kubectl apply -f -
kubectl -n tearenv-system create secret generic tearenvd-ssh-user-ca \
  --from-file=ca_key=./tearenv_user_ca
kubectl apply -k deploy/kustomize/overlays/oidc
```

Store the CA key in a secret manager, remove the local copy securely, and
publish the registration API through HTTPS before allowing remote clients.
