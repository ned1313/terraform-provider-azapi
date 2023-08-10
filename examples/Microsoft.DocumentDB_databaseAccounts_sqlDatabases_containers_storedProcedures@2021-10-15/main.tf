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
    identity = {
      type = "None"
    }
    kind = "GlobalDocumentDB"
    properties = {
      capabilities = [
      ]
      consistencyPolicy = {
        defaultConsistencyLevel = "Session"
        maxIntervalInSeconds    = 5
        maxStalenessPrefix      = 100
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

resource "azapi_resource" "sqlDatabase" {
  type      = "Microsoft.DocumentDB/databaseAccounts/sqlDatabases@2021-10-15"
  parent_id = azapi_resource.databaseAccount.id
  name      = var.resource_name
  body = jsonencode({
    properties = {
      options = {
      }
      resource = {
        id = var.resource_name
      }
    }
  })
  schema_validation_enabled = false
  response_export_values    = ["*"]
}

resource "azapi_resource" "container" {
  type      = "Microsoft.DocumentDB/databaseAccounts/sqlDatabases/containers@2023-04-15"
  parent_id = azapi_resource.sqlDatabase.id
  name      = var.resource_name
  body = jsonencode({
    properties = {
      options = {
      }
      resource = {
        id = var.resource_name
        partitionKey = {
          kind = "Hash"
          paths = [
            "/definition/id",
          ]
        }
      }
    }
  })
  schema_validation_enabled = false
  response_export_values    = ["*"]
}

resource "azapi_resource" "storedProcedure" {
  type      = "Microsoft.DocumentDB/databaseAccounts/sqlDatabases/containers/storedProcedures@2021-10-15"
  parent_id = azapi_resource.container.id
  name      = var.resource_name
  body = jsonencode({
    properties = {
      options = {
      }
      resource = {
        body = "  \tfunction () {\n\t\tvar context = getContext();\n\t\tvar response = context.getResponse();\n\t\tresponse.setBody('Hello, World');\n\t}\n"
        id   = var.resource_name
      }
    }
  })
  schema_validation_enabled = false
  response_export_values    = ["*"]
}

