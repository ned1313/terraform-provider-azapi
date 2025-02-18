package services

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/terraform-provider-azapi/internal/azure"
	"github.com/Azure/terraform-provider-azapi/internal/azure/identity"
	"github.com/Azure/terraform-provider-azapi/internal/azure/location"
	"github.com/Azure/terraform-provider-azapi/internal/azure/tags"
	aztypes "github.com/Azure/terraform-provider-azapi/internal/azure/types"
	"github.com/Azure/terraform-provider-azapi/internal/clients"
	"github.com/Azure/terraform-provider-azapi/internal/locks"
	"github.com/Azure/terraform-provider-azapi/internal/services/defaults"
	"github.com/Azure/terraform-provider-azapi/internal/services/dynamic"
	"github.com/Azure/terraform-provider-azapi/internal/services/myplanmodifier"
	"github.com/Azure/terraform-provider-azapi/internal/services/myvalidator"
	"github.com/Azure/terraform-provider-azapi/internal/services/parse"
	"github.com/Azure/terraform-provider-azapi/internal/tf"
	"github.com/Azure/terraform-provider-azapi/utils"
	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

type AzapiResourceModel struct {
	ID                      types.String   `tfsdk:"id"`
	Name                    types.String   `tfsdk:"name"`
	ParentID                types.String   `tfsdk:"parent_id"`
	Type                    types.String   `tfsdk:"type"`
	Location                types.String   `tfsdk:"location"`
	Identity                types.List     `tfsdk:"identity"`
	Body                    types.String   `tfsdk:"body"`
	Payload                 types.Dynamic  `tfsdk:"payload"`
	Locks                   types.List     `tfsdk:"locks"`
	RemovingSpecialChars    types.Bool     `tfsdk:"removing_special_chars"`
	SchemaValidationEnabled types.Bool     `tfsdk:"schema_validation_enabled"`
	IgnoreBodyChanges       types.List     `tfsdk:"ignore_body_changes"`
	IgnoreCasing            types.Bool     `tfsdk:"ignore_casing"`
	IgnoreMissingProperty   types.Bool     `tfsdk:"ignore_missing_property"`
	ResponseExportValues    types.List     `tfsdk:"response_export_values"`
	Output                  types.String   `tfsdk:"output"`
	OutputPayload           types.Dynamic  `tfsdk:"output_payload"`
	Tags                    types.Map      `tfsdk:"tags"`
	Timeouts                timeouts.Value `tfsdk:"timeouts"`
}

var _ resource.Resource = &AzapiResource{}
var _ resource.ResourceWithConfigure = &AzapiResource{}
var _ resource.ResourceWithModifyPlan = &AzapiResource{}
var _ resource.ResourceWithValidateConfig = &AzapiResource{}
var _ resource.ResourceWithImportState = &AzapiResource{}

type AzapiResource struct {
	ProviderData *clients.Client
}

func (r *AzapiResource) Configure(_ context.Context, request resource.ConfigureRequest, _ *resource.ConfigureResponse) {
	if v, ok := request.ProviderData.(*clients.Client); ok {
		r.ProviderData = v
	}
}

func (r *AzapiResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_resource"
}

func (r *AzapiResource) Schema(ctx context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},

			"name": schema.StringAttribute{
				Optional: true,
				Computed: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},

			"removing_special_chars": schema.BoolAttribute{
				DeprecationMessage: "This feature is deprecated and will be removed in a major release. Please use the `name` argument to specify the name of the resource.",
				Optional:           true,
				Computed:           true,
				Default:            defaults.BoolDefault(false),
			},

			"parent_id": schema.StringAttribute{
				Optional: true,
				Computed: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				Validators: []validator.String{
					myvalidator.StringIsResourceID(),
				},
			},

			"type": schema.StringAttribute{
				Required: true,
				Validators: []validator.String{
					myvalidator.StringIsResourceType(),
				},
			},

			"location": schema.StringAttribute{
				Optional: true,
				Computed: true,
				PlanModifiers: []planmodifier.String{
					myplanmodifier.UseStateWhen(func(a, b types.String) bool {
						return location.Normalize(a.ValueString()) == location.Normalize(b.ValueString())
					}),
				},
			},

			"payload": schema.DynamicAttribute{
				Optional: true,
				PlanModifiers: []planmodifier.Dynamic{
					myplanmodifier.DynamicUseStateWhen(dynamic.SemanticallyEqual),
				},
			},

			"body": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Default:  defaults.StringDefault("{}"),
				Validators: []validator.String{
					myvalidator.StringIsJSON(),
				},
				PlanModifiers: []planmodifier.String{
					myplanmodifier.UseStateWhen(func(a, b types.String) bool {
						return utils.NormalizeJson(a.ValueString()) == utils.NormalizeJson(b.ValueString())
					}),
				},
				DeprecationMessage: "This feature is deprecated and will be removed in a major release. Please use the `payload` argument to specify the body of the resource.",
			},

			"ignore_body_changes": schema.ListAttribute{
				ElementType: types.StringType,
				Optional:    true,
				Validators: []validator.List{
					listvalidator.ValueStringsAre(myvalidator.StringIsNotEmpty()),
				},
				DeprecationMessage: "This feature is deprecated and will be removed in a major release. Please use the `lifecycle.ignore_changes` argument to specify the fields in `payload` to ignore.",
			},

			"ignore_casing": schema.BoolAttribute{
				Optional:           true,
				Computed:           true,
				Default:            defaults.BoolDefault(false),
				DeprecationMessage: "This feature is deprecated and will be removed in a major release. Please use the `lifecycle.ignore_changes` argument to specify the fields in `payload` to ignore.",
			},

			"ignore_missing_property": schema.BoolAttribute{
				Optional: true,
				Computed: true,
				Default:  defaults.BoolDefault(true),
			},

			"response_export_values": schema.ListAttribute{
				ElementType: types.StringType,
				Optional:    true,
				Validators: []validator.List{
					listvalidator.ValueStringsAre(myvalidator.StringIsNotEmpty()),
				},
			},

			"locks": schema.ListAttribute{
				ElementType: types.StringType,
				Optional:    true,
				Validators: []validator.List{
					listvalidator.ValueStringsAre(myvalidator.StringIsNotEmpty()),
				},
			},

			"schema_validation_enabled": schema.BoolAttribute{
				Optional: true,
				Computed: true,
				Default:  defaults.BoolDefault(true),
			},

			"output": schema.StringAttribute{
				Computed:           true,
				DeprecationMessage: "This feature is deprecated and will be removed in a major release. Please use the `output_payload` argument to output the response of the resource.",
			},

			"output_payload": schema.DynamicAttribute{
				Computed: true,
			},

			"tags": schema.MapAttribute{
				ElementType: types.StringType,
				Optional:    true,
				Computed:    true,
				Validators: []validator.Map{
					tags.Validator(),
				},
			},
		},
		Blocks: map[string]schema.Block{
			"identity": schema.ListNestedBlock{
				NestedObject: schema.NestedBlockObject{
					Attributes: map[string]schema.Attribute{
						"type": schema.StringAttribute{
							Required: true,
							Validators: []validator.String{stringvalidator.OneOf(
								string(identity.SystemAssignedUserAssigned),
								string(identity.UserAssigned),
								string(identity.SystemAssigned),
								string(identity.None),
							)},
						},

						"identity_ids": schema.ListAttribute{
							ElementType: types.StringType,
							Optional:    true,
							Validators: []validator.List{
								listvalidator.ValueStringsAre(myvalidator.StringIsUserAssignedIdentityID()),
							},
						},

						"principal_id": schema.StringAttribute{
							Computed: true,
						},

						"tenant_id": schema.StringAttribute{
							Computed: true,
						},
					},
				},
			},

			"timeouts": timeouts.Block(ctx, timeouts.Opts{
				Create: true,
				Read:   true,
				Delete: true,
			}),
		},
		Version: 0,
	}
}

func (r *AzapiResource) ValidateConfig(ctx context.Context, request resource.ValidateConfigRequest, response *resource.ValidateConfigResponse) {
	var config *AzapiResourceModel
	if response.Diagnostics.Append(request.Config.Get(ctx, &config)...); response.Diagnostics.HasError() {
		return
	}
	// destroy doesn't need to modify plan
	if config == nil {
		return
	}

	// can't specify both body and payload
	if !config.Body.IsNull() && !config.Payload.IsNull() {
		response.Diagnostics.AddError("Invalid config", "can't specify both body and payload")
		return
	}

	resourceType := config.Type.ValueString()

	// for resource group, if parent_id is not specified, set it to subscription id
	if config.ParentID.IsNull() {
		azureResourceType, _, _ := utils.GetAzureResourceTypeApiVersion(resourceType)
		if !strings.EqualFold(azureResourceType, arm.ResourceGroupResourceType.String()) {
			response.Diagnostics.AddError("Missing required argument", `The argument "parent_id" is required, but no definition was found.`)
			return
		}
	}

	if config.Body.IsUnknown() {
		return
	}

	body := make(map[string]interface{})
	if bodyValueString := config.Body.ValueString(); bodyValueString != "" {
		if err := json.Unmarshal([]byte(bodyValueString), &body); err != nil {
			response.Diagnostics.AddError("Invalid JSON string", fmt.Sprintf(`The argument "body" is invalid: value: %s, err: %+v`, bodyValueString, err))
			return
		}
	}
	if diags := validateDuplicatedDefinitions(config, body); diags.HasError() {
		response.Diagnostics.Append(diags...)
		return
	}
}

func (r *AzapiResource) ModifyPlan(ctx context.Context, request resource.ModifyPlanRequest, response *resource.ModifyPlanResponse) {
	var config, state, plan *AzapiResourceModel
	response.Diagnostics.Append(request.Config.Get(ctx, &config)...)
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}

	// destroy doesn't need to modify plan
	if config == nil {
		return
	}

	defer func() {
		response.Plan.Set(ctx, plan)
	}()

	// Output is a computed field, it defaults to unknown if there's any plan change
	// It sets to the state if the state exists, and will set to unknown if the output needs to be updated
	if state != nil {
		plan.Output = state.Output
		plan.OutputPayload = state.OutputPayload
	}
	resourceType := config.Type.ValueString()

	// for resource group, if parent_id is not specified, set it to subscription id
	if config.ParentID.IsNull() {
		azureResourceType, _, _ := utils.GetAzureResourceTypeApiVersion(resourceType)
		if strings.EqualFold(azureResourceType, arm.ResourceGroupResourceType.String()) {
			plan.ParentID = types.StringValue(fmt.Sprintf("/subscriptions/%s", r.ProviderData.Account.GetSubscriptionId()))
		}
	}

	if name, diags := r.nameWithDefaultNaming(config.Name); !diags.HasError() {
		plan.Name = name
		// replace the resource if the name is changed
		if state != nil && !state.Name.Equal(plan.Name) {
			response.RequiresReplace.Append(path.Root("name"))
		}
	} else {
		response.Diagnostics.Append(diags...)
		return
	}

	// if the config identity type and identity ids are not changed, use the state identity
	if !config.Identity.IsNull() && state != nil && !state.Identity.IsNull() {
		configIdentity := identity.FromList(config.Identity)
		stateIdentity := identity.FromList(state.Identity)
		if configIdentity.Type.Equal(stateIdentity.Type) && configIdentity.IdentityIDs.Equal(stateIdentity.IdentityIDs) {
			plan.Identity = state.Identity
		}
	}

	if plan.Body.IsUnknown() || plan.Payload.IsUnknown() {
		if config.Tags.IsNull() {
			plan.Tags = basetypes.NewMapUnknown(types.StringType)
		}
		if config.Location.IsNull() {
			plan.Location = basetypes.NewStringUnknown()
		}
		plan.Output = types.StringUnknown()
		plan.OutputPayload = basetypes.NewDynamicUnknown()
		return
	}

	if state == nil || !plan.Identity.Equal(state.Identity) || !plan.ResponseExportValues.Equal(state.ResponseExportValues) ||
		utils.NormalizeJson(plan.Body.ValueString()) != utils.NormalizeJson(state.Body.ValueString()) ||
		!plan.Payload.Equal(state.Payload) {
		plan.Output = types.StringUnknown()
		plan.OutputPayload = basetypes.NewDynamicUnknown()
	}

	var body map[string]interface{}
	switch {
	case !config.Payload.IsNull():
		out, err := expandPayload(config.Payload)
		if err != nil {
			response.Diagnostics.AddError("Invalid configuration", fmt.Sprintf(`The argument "payload" is invalid: value: %s, err: %+v`, config.Payload.String(), err))
			return
		}
		body = out
	case !config.Body.IsNull():
		bodyValueString := plan.Body.ValueString()
		err := json.Unmarshal([]byte(bodyValueString), &body)
		if err != nil {
			response.Diagnostics.AddError("Invalid JSON string", fmt.Sprintf(`The argument "body" is invalid: value: %s, err: %+v`, plan.Body.ValueString(), err))
			return
		}
	default:
		body = map[string]interface{}{}
	}

	azureResourceType, apiVersion, err := utils.GetAzureResourceTypeApiVersion(config.Type.ValueString())
	if err != nil {
		response.Diagnostics.AddError("Invalid configuration", fmt.Sprintf(`The argument "type" is invalid: %s`, err.Error()))
		return
	}
	resourceDef, _ := azure.GetResourceDefinition(azureResourceType, apiVersion)

	plan.Tags = r.tagsWithDefaultTags(config.Tags, body, state, resourceDef)
	if state == nil || !state.Tags.Equal(plan.Tags) {
		plan.Output = types.StringUnknown()
		plan.OutputPayload = basetypes.NewDynamicUnknown()
	}

	// location field has a field level plan modifier which suppresses the diff if the location is not actually changed
	locationValue := plan.Location
	// For the following cases, we need to use the location in config as the specified location
	// case 1. To create a new resource, the location is not specified in config, then the planned location will be unknown
	// case 2. To update a resource, the location is not specified in config, then the planned location will be the state location
	if locationValue.IsUnknown() || config.Location.IsNull() {
		locationValue = config.Location
	}
	// locationWithDefaultLocation will return the location in config if it's not null, otherwise it will return the default location if it supports location
	plan.Location = r.locationWithDefaultLocation(locationValue, body, state, resourceDef)
	if state != nil && location.Normalize(state.Location.ValueString()) != location.Normalize(plan.Location.ValueString()) {
		// if the location is changed, replace the resource
		response.RequiresReplace.Append(path.Root("location"))
	}
	if plan.SchemaValidationEnabled.ValueBool() {
		if response.Diagnostics.Append(expandBody(body, *plan)...); response.Diagnostics.HasError() {
			return
		}
		body["name"] = plan.Name.ValueString()
		err = schemaValidation(azureResourceType, apiVersion, resourceDef, body)
		if err != nil {
			response.Diagnostics.AddError("Invalid configuration", err.Error())
			return
		}
	}
}

func (r *AzapiResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	r.CreateUpdate(ctx, request.Plan, &response.State, &response.Diagnostics)
}

func (r *AzapiResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	r.CreateUpdate(ctx, request.Plan, &response.State, &response.Diagnostics)
}

func (r *AzapiResource) CreateUpdate(ctx context.Context, requestPlan tfsdk.Plan, responseState *tfsdk.State, diagnostics *diag.Diagnostics) {
	var plan, state *AzapiResourceModel
	diagnostics.Append(requestPlan.Get(ctx, &plan)...)
	diagnostics.Append(responseState.Get(ctx, &state)...)
	if diagnostics.HasError() {
		return
	}

	createUpdateTimeout, diags := plan.Timeouts.Create(ctx, 30*time.Minute)
	diagnostics.Append(diags...)
	if diagnostics.HasError() {
		return
	}

	ctx, cancel := context.WithTimeout(ctx, createUpdateTimeout)
	defer cancel()

	id, err := parse.NewResourceID(plan.Name.ValueString(), plan.ParentID.ValueString(), plan.Type.ValueString())
	if err != nil {
		diagnostics.AddError("Invalid configuration", err.Error())
		return
	}

	client := r.ProviderData.ResourceClient
	isNewResource := responseState == nil || responseState.Raw.IsNull()
	if isNewResource {
		// check if the resource already exists
		_, err = client.Get(ctx, id.AzureResourceId, id.ApiVersion)
		if err == nil {
			diagnostics.AddError("Resource already exists", tf.ImportAsExistsError("azapi_resource", id.ID()).Error())
			return
		}
		if !utils.ResponseErrorWasNotFound(err) {
			diagnostics.AddError("Failed to retrieve resource", fmt.Errorf("checking for presence of existing %s: %+v", id, err).Error())
			return
		}
	}

	// build the request body
	var body map[string]interface{}
	switch {
	case !plan.Payload.IsNull():
		out, err := expandPayload(plan.Payload)
		if err != nil {
			diagnostics.AddError("Invalid payload", err.Error())
			return
		}
		body = out
	case !plan.Body.IsNull():
		bodyValueString := plan.Body.ValueString()
		err := json.Unmarshal([]byte(bodyValueString), &body)
		if err != nil {
			diagnostics.AddError("Invalid JSON string", fmt.Sprintf(`The argument "body" is invalid: value: %s, err: %+v`, plan.Body.ValueString(), err))
			return
		}
	default:
		body = map[string]interface{}{}
	}
	if diagnostics.Append(expandBody(body, *plan)...); diagnostics.HasError() {
		return
	}

	if !isNewResource {
		// handle the case that identity block was once set, now it's removed
		if stateIdentity := identity.FromList(state.Identity); body["identity"] == nil && stateIdentity.Type.ValueString() != string(identity.None) {
			noneIdentity := identity.Model{Type: types.StringValue(string(identity.None))}
			out, _ := identity.ExpandIdentity(noneIdentity)
			body["identity"] = out
		}

		// handle the case that `ignore_body_changes` is set
		if ignoreChanges := AsStringList(plan.IgnoreBodyChanges); len(ignoreChanges) != 0 {
			// retrieve the existing resource
			existing, err := client.Get(ctx, id.AzureResourceId, id.ApiVersion)
			if err != nil {
				diagnostics.AddError("Failed to retrieve resource", fmt.Errorf("reading %s: %+v", id, err).Error())
				return
			}

			merged, err := overrideWithPaths(body, existing, ignoreChanges)
			if err != nil {
				diagnostics.AddError("Invalid configuration", fmt.Sprintf(`The argument "ignore_body_changes" is invalid: value: %s, err: %+v`, plan.IgnoreBodyChanges.String(), err))
				return
			}

			if id.ResourceDef != nil {
				merged = (*id.ResourceDef).GetWriteOnly(utils.NormalizeObject(merged))
			}

			body = merged.(map[string]interface{})
		}
	}

	// create/update the resource
	for _, lockId := range AsStringList(plan.Locks) {
		locks.ByID(lockId)
		defer locks.UnlockByID(lockId)
	}

	responseBody, err := client.CreateOrUpdate(ctx, id.AzureResourceId, id.ApiVersion, body)
	if err != nil {
		diagnostics.AddError("Failed to create/update resource", fmt.Errorf("creating/updating %s: %+v", id, err).Error())
		return
	}

	// generate the computed fields
	plan.ID = types.StringValue(id.ID())
	plan.Output = types.StringValue(flattenOutput(responseBody, AsStringList(plan.ResponseExportValues)))
	plan.OutputPayload = types.DynamicValue(flattenOutputPayload(responseBody, AsStringList(plan.ResponseExportValues)))
	if bodyMap, ok := responseBody.(map[string]interface{}); ok {
		if !plan.Identity.IsNull() {
			planIdentity := identity.FromList(plan.Identity)
			if v := identity.FlattenIdentity(bodyMap["identity"]); v != nil {
				planIdentity.TenantID = v.TenantID
				planIdentity.PrincipalID = v.PrincipalID
			} else {
				planIdentity.TenantID = types.StringNull()
				planIdentity.PrincipalID = types.StringNull()
			}
			plan.Identity = identity.ToList(planIdentity)
		}
	}

	diagnostics.Append(responseState.Set(ctx, plan)...)
}

func (r *AzapiResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	var model AzapiResourceModel
	if response.Diagnostics.Append(request.State.Get(ctx, &model)...); response.Diagnostics.HasError() {
		return
	}

	readTimeout, diags := model.Timeouts.Read(ctx, 5*time.Minute)
	response.Diagnostics.Append(diags...)
	if response.Diagnostics.HasError() {
		return
	}

	ctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()

	id, err := parse.ResourceIDWithResourceType(model.ID.ValueString(), model.Type.ValueString())
	if err != nil {
		response.Diagnostics.AddError("Error parsing ID", err.Error())
		return
	}

	client := r.ProviderData.ResourceClient
	responseBody, err := client.Get(ctx, id.AzureResourceId, id.ApiVersion)
	if err != nil {
		if utils.ResponseErrorWasNotFound(err) {
			tflog.Info(ctx, fmt.Sprintf("Error reading %q - removing from state", id.ID()))
			response.State.RemoveResource(ctx)
			return
		}
		response.Diagnostics.AddError("Failed to retrieve resource", fmt.Errorf("reading %s: %+v", id, err).Error())
		return
	}

	state := model
	state.Name = types.StringValue(id.Name)
	state.ParentID = types.StringValue(id.ParentId)
	state.Type = types.StringValue(fmt.Sprintf("%s@%s", id.AzureResourceType, id.ApiVersion))

	var requestBody map[string]interface{}
	var useBody bool
	switch {
	case !model.Payload.IsNull():
		useBody = false
		out, err := expandPayload(model.Payload)
		if err != nil {
			response.Diagnostics.AddError("Invalid payload", err.Error())
			return
		}
		requestBody = out
	case !model.Body.IsNull():
		useBody = true
		bodyValueString := model.Body.ValueString()
		err := json.Unmarshal([]byte(bodyValueString), &requestBody)
		if err != nil {
			response.Diagnostics.AddError("Invalid JSON string", fmt.Sprintf(`The argument "body" is invalid: value: %s, err: %+v`, model.Body.ValueString(), err))
			return
		}
	default:
		requestBody = map[string]interface{}{}
	}

	if bodyMap, ok := responseBody.(map[string]interface{}); ok {
		if v, ok := bodyMap["location"]; ok && location.Normalize(v.(string)) != location.Normalize(model.Location.ValueString()) {
			state.Location = types.StringValue(v.(string))
		}
		if output := tags.FlattenTags(bodyMap["tags"]); len(output.Elements()) != 0 || len(state.Tags.Elements()) != 0 {
			state.Tags = output
		}
		if requestBody["identity"] == nil {
			// The following codes are used to reflect the actual changes of identity when it's not configured inside the body.
			// And it suppresses the diff of nil identity and identity whose type is none.
			identityFromResponse := identity.FlattenIdentity(bodyMap["identity"])
			switch {
			// Identity is not specified in config, and it's not in the response
			case state.Identity.IsNull() && (identityFromResponse == nil || identityFromResponse.Type.ValueString() == string(identity.None)):
				state.Identity = basetypes.NewListNull(identity.Model{}.ModelType())

			// Identity is not specified in config, but it's in the response
			case state.Identity.IsNull() && identityFromResponse != nil && identityFromResponse.Type.ValueString() != string(identity.None):
				state.Identity = identity.ToList(*identityFromResponse)

			// Identity is specified in config, but it's not in the response
			case !state.Identity.IsNull() && identityFromResponse == nil:
				stateIdentity := identity.FromList(state.Identity)
				// skip when the configured identity type is none
				if stateIdentity.Type.ValueString() == string(identity.None) {
					// do nothing
				} else {
					state.Identity = basetypes.NewListNull(identity.Model{}.ModelType())
				}

			// Identity is specified in config, and it's in the response
			case !state.Identity.IsNull() && identityFromResponse != nil:
				stateIdentity := identity.FromList(state.Identity)
				// suppress the diff of identity_ids = [] and identity_ids = null
				if len(stateIdentity.IdentityIDs.Elements()) == 0 && len(identityFromResponse.IdentityIDs.Elements()) == 0 {
					// to suppress the diff of identity_ids = [] and identity_ids = null
					identityFromResponse.IdentityIDs = stateIdentity.IdentityIDs
				}
				state.Identity = identity.ToList(*identityFromResponse)
			}
		}
	}
	state.Output = types.StringValue(flattenOutput(responseBody, AsStringList(model.ResponseExportValues)))
	state.OutputPayload = types.DynamicValue(flattenOutputPayload(responseBody, AsStringList(model.ResponseExportValues)))

	if ignoreBodyChanges := AsStringList(model.IgnoreBodyChanges); len(ignoreBodyChanges) != 0 {
		if out, err := overrideWithPaths(responseBody, requestBody, ignoreBodyChanges); err == nil {
			responseBody = out
		} else {
			response.Diagnostics.AddError("Invalid configuration", fmt.Sprintf(`The argument "ignore_body_changes" is invalid: value: %s, err: %+v`, model.IgnoreBodyChanges.String(), err))
			return
		}
	}

	option := utils.UpdateJsonOption{
		IgnoreCasing:          model.IgnoreCasing.ValueBool(),
		IgnoreMissingProperty: model.IgnoreMissingProperty.ValueBool(),
	}
	body := utils.UpdateObject(requestBody, responseBody, option)

	data, err := json.Marshal(body)
	if err != nil {
		response.Diagnostics.AddError("Invalid body", err.Error())
		return
	}
	if useBody {
		state.Body = types.StringValue(string(data))
	} else {
		payload, err := dynamic.FromJSON(data, model.Payload.UnderlyingValue().Type(ctx))
		if err != nil {
			tflog.Warn(ctx, fmt.Sprintf("Failed to parse payload: %s", err.Error()))
			payload, err = dynamic.FromJSONImplied(data)
			if err != nil {
				response.Diagnostics.AddError("Invalid payload", err.Error())
				return
			}
		}
		state.Payload = payload
	}

	response.Diagnostics.Append(response.State.Set(ctx, state)...)
}

func (r *AzapiResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	client := r.ProviderData.ResourceClient

	var model *AzapiResourceModel
	if response.Diagnostics.Append(request.State.Get(ctx, &model)...); response.Diagnostics.HasError() {
		return
	}

	deleteTimeout, diags := model.Timeouts.Delete(ctx, 30*time.Minute)
	response.Diagnostics.Append(diags...)
	if response.Diagnostics.HasError() {
		return
	}

	ctx, cancel := context.WithTimeout(ctx, deleteTimeout)
	defer cancel()

	id, err := parse.ResourceIDWithResourceType(model.ID.ValueString(), model.Type.ValueString())
	if err != nil {
		response.Diagnostics.AddError("Error parsing ID", err.Error())
		return
	}

	for _, lockId := range AsStringList(model.Locks) {
		locks.ByID(lockId)
		defer locks.UnlockByID(lockId)
	}

	_, err = client.Delete(ctx, id.AzureResourceId, id.ApiVersion)
	if err != nil && !utils.ResponseErrorWasNotFound(err) {
		response.Diagnostics.AddError("Failed to delete resource", fmt.Errorf("deleting %s: %+v", id, err).Error())
	}
}

func (r *AzapiResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	tflog.Debug(ctx, fmt.Sprintf("Importing Resource - parsing %q", request.ID))

	input := request.ID
	idUrl, err := url.Parse(input)
	if err != nil {
		response.Diagnostics.AddError("Invalid Resource ID", fmt.Errorf("parsing Resource ID %q: %+v", input, err).Error())
		return
	}
	apiVersion := idUrl.Query().Get("api-version")
	if apiVersion == "" {
		resourceType := utils.GetResourceType(input)
		apiVersions := azure.GetApiVersions(resourceType)
		if len(apiVersions) != 0 {
			input = fmt.Sprintf("%s?api-version=%s", input, apiVersions[len(apiVersions)-1])
		}
	}

	id, err := parse.ResourceIDWithApiVersion(input)
	if err != nil {
		response.Diagnostics.AddError("Invalid Resource ID", fmt.Errorf("parsing Resource ID %q: %+v", input, err).Error())
		return
	}

	client := r.ProviderData.ResourceClient

	state := AzapiResourceModel{
		ID:                      types.StringValue(id.ID()),
		Name:                    types.StringValue(id.Name),
		ParentID:                types.StringValue(id.ParentId),
		Type:                    types.StringValue(fmt.Sprintf("%s@%s", id.AzureResourceType, id.ApiVersion)),
		Locks:                   types.ListNull(types.StringType),
		Identity:                types.ListNull(identity.Model{}.ModelType()),
		Body:                    types.StringValue("{}"),
		RemovingSpecialChars:    types.BoolValue(false),
		SchemaValidationEnabled: types.BoolValue(true),
		IgnoreBodyChanges:       types.ListNull(types.StringType),
		IgnoreCasing:            types.BoolValue(false),
		IgnoreMissingProperty:   types.BoolValue(true),
		ResponseExportValues:    types.ListNull(types.StringType),
		Output:                  types.StringValue("{}"),
		OutputPayload:           types.DynamicNull(),
		Tags:                    types.MapNull(types.StringType),
		Timeouts: timeouts.Value{
			Object: types.ObjectNull(map[string]attr.Type{
				"create": types.StringType,
				"read":   types.StringType,
				"delete": types.StringType,
			}),
		},
	}

	responseBody, err := client.Get(ctx, id.AzureResourceId, id.ApiVersion)
	if err != nil {
		if utils.ResponseErrorWasNotFound(err) {
			tflog.Info(ctx, fmt.Sprintf("[INFO] Error reading %q - removing from state", id.ID()))
			response.State.RemoveResource(ctx)
			return
		}
		response.Diagnostics.AddError("Failed to retrieve resource", fmt.Errorf("reading %s: %+v", id, err).Error())
		return
	}

	tflog.Info(ctx, fmt.Sprintf("resource %q is imported", id.ID()))
	if id.ResourceDef != nil {
		writeOnlyBody := (*id.ResourceDef).GetWriteOnly(utils.NormalizeObject(responseBody))
		if bodyMap, ok := writeOnlyBody.(map[string]interface{}); ok {
			delete(bodyMap, "location")
			delete(bodyMap, "tags")
			delete(bodyMap, "name")
			delete(bodyMap, "identity")
			writeOnlyBody = bodyMap
		}
		data, err := json.Marshal(writeOnlyBody)
		if err != nil {
			response.Diagnostics.AddError("Invalid body", err.Error())
			return
		}
		payload, err := dynamic.FromJSONImplied(data)
		if err != nil {
			response.Diagnostics.AddError("Invalid payload", err.Error())
			return
		}
		state.Payload = payload
	} else {
		data, err := json.Marshal(responseBody)
		if err != nil {
			response.Diagnostics.AddError("Invalid body", err.Error())
			return
		}
		payload, err := dynamic.FromJSONImplied(data)
		if err != nil {
			response.Diagnostics.AddError("Invalid payload", err.Error())
			return
		}
		state.Payload = payload
	}
	if bodyMap, ok := responseBody.(map[string]interface{}); ok {
		if v, ok := bodyMap["location"]; ok {
			state.Location = types.StringValue(location.Normalize(v.(string)))
		}
		if output := tags.FlattenTags(bodyMap["tags"]); len(output.Elements()) != 0 {
			state.Tags = output
		}
		if v := identity.FlattenIdentity(bodyMap["identity"]); v != nil {
			state.Identity = identity.ToList(*v)
		}
	}

	response.Diagnostics.Append(response.State.Set(ctx, state)...)
}

func (r *AzapiResource) nameWithDefaultNaming(config types.String) (types.String, diag.Diagnostics) {
	if !config.IsNull() {
		return config, diag.Diagnostics{}
	}
	if r.ProviderData.Features.DefaultNaming != "" {
		return types.StringValue(r.ProviderData.Features.DefaultNaming), diag.Diagnostics{}
	}
	return types.StringNull(), diag.Diagnostics{
		diag.NewErrorDiagnostic("Missing required argument", `The argument "name" is required, but no definition was found.`),
	}
}

func (r *AzapiResource) tagsWithDefaultTags(config types.Map, body map[string]interface{}, state *AzapiResourceModel, resourceDef *aztypes.ResourceType) types.Map {
	if config.IsNull() {
		switch {
		case body["tags"] != nil:
			return tags.FlattenTags(body["tags"])
		case len(r.ProviderData.Features.DefaultTags) != 0 && canResourceHaveProperty(resourceDef, "tags"):
			defaultTags := r.ProviderData.Features.DefaultTags
			if state == nil || state.Tags.IsNull() {
				return tags.FlattenTags(defaultTags)
			} else {
				currentTags := tags.ExpandTags(state.Tags)
				if !reflect.DeepEqual(currentTags, defaultTags) {
					return tags.FlattenTags(defaultTags)
				} else {
					return state.Tags
				}
			}
		// To suppress the diff of config: tags = null and state: tags = {}
		case state != nil && !state.Tags.IsUnknown() && len(state.Tags.Elements()) == 0:
			return state.Tags
		}
	}
	return config
}

func (r *AzapiResource) locationWithDefaultLocation(config types.String, body map[string]interface{}, state *AzapiResourceModel, resourceDef *aztypes.ResourceType) types.String {
	if config.IsNull() {
		switch {
		case body["location"] != nil:
			return types.StringValue(body["location"].(string))
		case len(r.ProviderData.Features.DefaultLocation) != 0 && canResourceHaveProperty(resourceDef, "location"):
			defaultLocation := r.ProviderData.Features.DefaultLocation
			if state == nil || state.Location.IsNull() {
				return types.StringValue(defaultLocation)
			} else {
				currentLocation := state.Location.ValueString()
				if location.Normalize(currentLocation) != location.Normalize(defaultLocation) {
					return types.StringValue(defaultLocation)
				} else {
					return state.Location
				}
			}
		}
	}
	return config
}

func expandBody(body map[string]interface{}, model AzapiResourceModel) diag.Diagnostics {
	if body == nil {
		return diag.Diagnostics{}
	}
	if body["location"] == nil && !model.Location.IsNull() && !model.Location.IsUnknown() {
		body["location"] = model.Location.ValueString()
	}
	if body["tags"] == nil && !model.Tags.IsNull() && !model.Tags.IsUnknown() {
		body["tags"] = tags.ExpandTags(model.Tags)
	}
	if body["identity"] == nil && !model.Identity.IsNull() && !model.Identity.IsUnknown() {
		identityModel := identity.FromList(model.Identity)
		out, err := identity.ExpandIdentity(identityModel)
		if err != nil {
			return diag.Diagnostics{
				diag.NewErrorDiagnostic("Invalid configuration", fmt.Sprintf(`The argument "identity" is invalid: value: %s, err: %+v`, model.Identity.String(), err)),
			}
		}
		body["identity"] = out
	}
	return diag.Diagnostics{}
}

func validateDuplicatedDefinitions(model *AzapiResourceModel, body map[string]interface{}) diag.Diagnostics {
	diags := diag.Diagnostics{}
	if !model.Tags.IsNull() && !model.Tags.IsUnknown() && body["tags"] != nil {
		diags.AddError("Invalid configuration", `can't specify both the argument "tags" and "tags" in the argument "body"`)
	}
	if !model.Location.IsNull() && !model.Location.IsUnknown() && body["location"] != nil {
		diags.AddError("Invalid configuration", `can't specify both the argument "location" and "location" in the argument "body"`)
	}
	if !model.Identity.IsNull() && !model.Identity.IsUnknown() && body["identity"] != nil {
		diags.AddError("Invalid configuration", `can't specify both the argument "identity" and "identity" in the argument "body"`)
	}
	return diags
}

func expandPayload(input types.Dynamic) (map[string]interface{}, error) {
	data, err := dynamic.ToJSON(input)
	if err != nil {
		return nil, err
	}
	var out map[string]interface{}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}
