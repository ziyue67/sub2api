export function showUpstreamResponseModelToUsers(settings) {
    return settings?.usage_show_upstream_response_model_to_users === true;
}
// userVisibleUpstreamResponseModel returns the upstream response model when
// the admin has allowed users to see it and it differs from the requested model.
export function userVisibleUpstreamResponseModel(settings, row) {
    if (!showUpstreamResponseModelToUsers(settings) || !row)
        return '';
    const response = row.upstream_response_model?.trim() || '';
    const requested = row.model?.trim() || '';
    if (!response || response === requested)
        return '';
    return response;
}
