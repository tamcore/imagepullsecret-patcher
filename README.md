# imagepullsecret-patcher

[![Build Status](https://img.shields.io/github/actions/workflow/status/tamcore/imagepullsecret-patcher/ci.yaml?branch=master&label=ci&logo=github&style=flat-square)](https://github.com/tamcore/imagepullsecret-patcher/actions?workflow=Go)
[![Go Report Card](https://goreportcard.com/badge/github.com/tamcore/imagepullsecret-patcher)](https://goreportcard.com/report/github.com/tamcore/imagepullsecret-patcher)
![Codecov](https://img.shields.io/codecov/c/github/tamcore/imagepullsecret-patcher)
![GitHub tag (latest SemVer)](https://img.shields.io/github/v/tag/tamcore/imagepullsecret-patcher)
![GitHub issues](https://img.shields.io/github/issues/tamcore/imagepullsecret-patcher)

A simple Kubernetes controller, that creates and reconciles imagePullSecrets and attaches them to ServiceAccounts in all namespaces, to allow authenticated access to a private container registry.

## Installation and configuration

A helm chart is available in the [deploy](deploy/helm) directory.

```shell
# fetch chart version
skopeo list-tags docker://ghcr.io/tamcore/charts/imagepullsecret-patcher
# or
crane ls ghcr.io/tamcore/charts/imagepullsecret-patcher

# deploy
helm upgrade --install \
    imagepullsecret-patcher \
    oci://ghcr.io/tamcore/charts/imagepullsecret-patcher \
    --version ${CHART_VERSION} \
    --namespace ${NAMESPACE}
```

Available configuration options are

| Config name          | ENV                         | Command flag          | Default value          | Description                                                                                                                                                  |
| -------------------- | --------------------------- | --------------------- | -----------------------| -------------------------------------------------------------------------------------------------------------------------------------------------------------|
| debug                | CONFIG_DEBUG                | -debug                | false                  | show DEBUG logs                                                                                                                                              |
| serviceaccounts      | CONFIG_SERVICEACCOUNTS      | -serviceaccounts      | "default"              | comma-separated list of ServiceAccounts to reconcile                                                                                                             |
| dockerconfigjson     | CONFIG_DOCKERCONFIGJSON     | -dockerconfigjson     | ""                     | json credentials for authenticating to container registry                                                                                                        |
| dockerconfigjsonpath | CONFIG_DOCKERCONFIGJSONPATH | -dockerconfigjsonpath | ""                     | absolute path to mounted json credentials                                                                                              |
| secret name          | CONFIG_SECRETNAME           | -secretname           | "global-imagepullsecret"    | name of managed secrets                                                                                                                                      |
| excluded namespaces  | CONFIG_EXCLUDED_NAMESPACES  | -excluded-namespaces  | "kube-*"                     | comma-separated namespaces excluded from processing                                                                                                          |
And here are the annotations available:

| Annotation                                        | Object    | Description                                                                                                       |
| ------------------------------------------------- | --------- | ----------------------------------------------------------------------------------------------------------------- |
| pborn.eu/imagepullsecret-patcher-exclude | namespace, secret | If this annotation is set to `true`, the object is excluded from reconciling. |

## Providing credentials

The desired credentials (or to be more specific, contents of the `.dockerconfigjson`) can be provided in 2 ways.

Either by passing the environment variable `CONFIG_DOCKERCONFIGJSON` containing the raw json, or `CONFIG_DOCKERCONFIGJSONPATH` pointing to the path, where the controller can access the provided credentials from a file. For example from a Secret that has been mounted into the Pod.

The 2nd option also has the advantage, that mounted secrets can be dynamically updated. Therefore it is not required to restart the controller, when the secret is updated.

## Why

To deploy images from a private container registry, we have to provide Kubernetes with credentials to pull them. This is done by providing so called imagePullSecrets.

They're either attached to a
- `Pod`'s definition (https://kubernetes.io/docs/concepts/containers/images/#specifying-imagepullsecrets-on-a-pod)

This is done manually by executing the command for each namespace (kubectl create secret..) and each ServiceAccount in it (kubectl patch..)

```
kubectl create secret docker-registry image-pull-secret \
  -n <your-namespace> \
  --docker-server=<your-registry-server> \
  --docker-username=<your-name> \
  --docker-password=<your-pword> \
  --docker-email=<your-email>

kubectl patch serviceaccount default \
  -p "{\"imagePullSecrets\": [{\"name\": \"image-pull-secret\"}]}" \
  -n <your-namespace>
```

or.. we could automate with a small controller like this imagepullsecret-patcher.

Using the imagepullsecret-patcher also has the advantage, that deployments via ArgoCD for example are automatically caught and newly created ServiceAccounts are automatically patched, as the controller issues a WATCH on ServiceAccount resources and therefore is notified by Kubernetes, if something changes. The same goes for unwanted changes to managed Secrets. That way we can ensure they're not tampered with and always match our source.
