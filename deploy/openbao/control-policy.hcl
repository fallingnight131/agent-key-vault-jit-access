# Control service: write-only credential provisioning. No read/list/delete capability.
path "kv/data/credentials/*" {
  capabilities = ["create", "update"]
}
path "transit/keys/*" {
  capabilities = ["create", "update"]
}
path "database/roles/*" {
  capabilities = ["create", "update"]
}
