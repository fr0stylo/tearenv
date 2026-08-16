# Deprecated public-keys overlay

This directory now resolves to the default overlay without additional patches. Static public-key injection has been replaced by the authenticated `UserRegistration` API and `tearenv login` flow.

Use `../default` for new installations. The unreferenced `deployment-patch.yaml` remains only as historical source context and must not be applied directly.
