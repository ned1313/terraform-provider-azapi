terraform {
  required_providers {
    azapi = {
      source = "Azure/azapi"
    }
  }
}

provider "azapi" {
  skip_provider_registration = false
}

variable "resource_name" {
  type    = string
  default = "acctest0001"
}

variable "location" {
  type    = string
  default = "westeurope"
}

resource "azapi_resource" "resourceGroup" {
  type                      = "Microsoft.Resources/resourceGroups@2020-06-01"
  name                      = var.resource_name
  location                  = var.location
}

resource "azapi_resource" "databaseAccount" {
  type      = "Microsoft.DocumentDB/databaseAccounts@2021-10-15"
  parent_id = azapi_resource.resourceGroup.id
  name      = var.resource_name
  location  = var.location
  body = jsonencode({
    kind = "GlobalDocumentDB"
    properties = {
      capabilities = [
      ]
      consistencyPolicy = {
        defaultConsistencyLevel = "BoundedStaleness"
        maxIntervalInSeconds    = 10
        maxStalenessPrefix      = 200
      }
      databaseAccountOfferType           = "Standard"
      defaultIdentity                    = "FirstPartyIdentity"
      disableKeyBasedMetadataWriteAccess = false
      disableLocalAuth                   = false
      enableAnalyticalStorage            = false
      enableAutomaticFailover            = false
      enableFreeTier                     = false
      enableMultipleWriteLocations       = false
      ipRules = [
      ]
      isVirtualNetworkFilterEnabled = false
      locations = [
        {
          failoverPriority = 0
          isZoneRedundant  = false
          locationName     = "West Europe"
        },
      ]
      networkAclBypass = "None"
      networkAclBypassResourceIds = [
      ]
      publicNetworkAccess = "Enabled"
      virtualNetworkRules = [
      ]
    }
  })
  schema_validation_enabled = false
  response_export_values    = ["*"]
}

