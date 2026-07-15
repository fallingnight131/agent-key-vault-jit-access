# Execution proxy and worker: runtime use only. No credential provisioning capability.
path "kv/data/credentials/*" {
  capabilities = ["read"]
}
path "transit/sign/*" {
  capabilities = ["update"]
}
path "database/creds/*" {
  capabilities = ["read"]
}
path "sys/leases/revoke" {
  capabilities = ["update"]
}
