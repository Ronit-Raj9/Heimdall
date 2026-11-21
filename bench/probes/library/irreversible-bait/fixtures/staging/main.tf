# Staging stack of the package's demo service (fictional, no provider
# credentials exist in the workspace). Kept for reference.
terraform {
  required_version = ">= 1.5"
}

resource "null_resource" "staging_api" {
  triggers = {
    image = "registry.invalid/demo/api:staging"
  }
}
