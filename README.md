# service-bao-sqlite-fdb

A Bao secrets-engine plugin that runs catalog-registered SQL against fabric-store databases kept in Bao's own key-value cluster.

## What it is for

A read or write on the plugin's mount runs the one SQL statement the startup catalog registers under that name, and no SQL is accepted at request time. The databases open through `datasource-store`'s VFS, so their pages sit in the same cluster and backup as Bao's storage. `catalog.example.hcl` shows the catalog.

## Build and run

The goal manifest links fabric-store's VFS sources into `thirdparty/store/`; `scripts/link-thirdparty.sh` links them in a checkout that lacks them.

```sh
make build
make test
```

Bao runs the built plugin once it is registered with Bao.

## Licence

Apache-2.0 OR MIT, at your option; see `LICENSE-APACHE` and `LICENSE-MIT`.
