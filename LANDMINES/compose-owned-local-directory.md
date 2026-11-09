# Compose can create a root-owned local state directory

**Applies when:** Compose has created `.local/secrets/auth` through a bind mount
before the developer created `.local` themselves, and Kubernetes bootstrap uses
its default `.local/k8s` state directory.

**Symptom:** The Kubernetes secret generator fails with `mkdir: cannot create
directory .../.local/k8s: Permission denied`, even though repository files are
writable.

**Cause:** The Docker daemon can create missing bind-mount parent directories
as root. This was reproduced with `.local` owned by root and mode `0755`.

**Recovery:** Inspect `ls -ld .local` first. Either set `K8S_STATE_DIR` consistently
to a writable, private directory outside the tracked tree, or have an operator
create only the Kubernetes state directory, owned by the developer:

```sh
sudo install -d -o "$(id -u)" -g "$(id -g)" -m 700 "$PWD/.local/k8s"
```

Run this from the repository root after checking the path. Do not recursively
change ownership of `.local`, and do not replace the existing Compose signing
key. Bootstrap reports this recovery rather than silently escalating privileges.

**Remove this entry when:** Compose no longer creates root-owned bind-mount
parents, or the supported Kubernetes state location cannot overlap those parents.
