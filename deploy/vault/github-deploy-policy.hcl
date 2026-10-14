# Attach this policy to the renewable token stored as the GitHub environment
# VAULT_TOKEN. Keep the token scoped to deployment configuration and its own
# lease; do not grant it broad secret or root access.
path "secret/data/pingmessenger/platform/deploy" {
  capabilities = ["read"]
}

path "auth/token/lookup-self" {
  capabilities = ["read"]
}

path "auth/token/renew-self" {
  capabilities = ["update"]
}
