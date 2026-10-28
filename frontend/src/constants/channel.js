/** Channel status values (must match service.Status* constants in Go). */
export const CHANNEL_STATUS_ACTIVE = 'active';
export const CHANNEL_STATUS_DISABLED = 'disabled';
/** Billing mode values (must match service.BillingMode* constants in Go). */
export const BILLING_MODE_TOKEN = 'token';
export const BILLING_MODE_PER_REQUEST = 'per_request';
export const BILLING_MODE_IMAGE = 'image';
export const BILLING_MODE_VIDEO = 'video';
/** Supported request reasoning effort levels, ordered from lowest to highest. */
export const REASONING_EFFORT_LEVELS = ['none', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max'];
/** Billing-model-source values (must match service.BillingModelSource* constants in Go). */
export const BILLING_MODEL_SOURCE_REQUESTED = 'requested';
export const BILLING_MODEL_SOURCE_UPSTREAM = 'upstream';
export const BILLING_MODEL_SOURCE_CHANNEL_MAPPED = 'channel_mapped';
export const BILLING_MODEL_SOURCE_RESPONSE = 'response_model';
