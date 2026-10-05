# service-bao-sqlite-fdb

A Bao secrets-engine plugin that runs catalog-registered SQL against SQLite databases, kept in Bao's key-value cluster or in local files.

## What it is for

A query or exec path on the plugin's mount runs the one SQL statement the startup catalog registers under that name, and no SQL is accepted at request time. In fabric mode the databases open through `datasource-store`'s VFS, so their pages sit in the same cluster and backup as Bao's storage; in plain mode they are local SQLite files. `catalog.example.hcl` shows the catalog.

## Build and run

The goal manifest links fabric-store's VFS sources into `thirdparty/store/`; `scripts/link-thirdparty.sh` links them in a checkout that lacks them.

```sh
make build
make test
make sha256
bao plugin register -sha256=<sum> secret sqlite-fdb
bao secrets enable -path=sqlite-fdb sqlite-fdb
```

The plugin does not start without `BAO_SQLITE_FDB_CATALOG`; `backend/backend.go` names the variables that select fabric or plain mode.

## Licence

Apache-2.0 OR MIT, at your option; see `LICENSE-APACHE` and `LICENSE-MIT`.
