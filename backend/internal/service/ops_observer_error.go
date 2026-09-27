package service

import "context"

// ObserverErrorRequest extends the own-request view with diagnostic labels.
// Do not embed OpsErrorLog: resolution operators, key prefixes and raw upstream
// payloads are not part of the observer usage contract.
type ObserverErrorRequest struct {
	UserErrorRequest
	Phase            string `json:"phase"`
	Type             string `json:"type"`
	Severity         string `json:"severity"`
	Resolved         bool   `json:"resolved"`
	Owner            string `json:"error_owner"`
	Source           string `json:"error_source"`
	RequestID        string `json:"request_id"`
	ClientRequestID  string `json:"client_request_id"`
	UserID           *int64 `json:"user_id"`
	UserEmail        string `json:"user_email"`
	APIKeyID         *int64 `json:"api_key_id"`
	APIKeyName       string `json:"api_key_name"`
	APIKeyDeleted    bool   `json:"api_key_deleted"`
	AccountID        *int64 `json:"account_id"`
	AccountName      string `json:"account_name"`
	GroupID          *int64 `json:"group_id"`
	RequestPath      string `json:"request_path"`
	UpstreamEndpoint string `json:"upstream_endpoint"`
	RequestedModel   string `json:"requested_model"`
	UpstreamModel    string `json:"upstream_model"`
}

type ObserverErrorRequestDetail struct {
	ObserverErrorRequest
	ErrorBody          string `json:"error_body"`
	UpstreamStatusCode *int   `json:"upstream_status_code,omitempty"`
	IsBusinessLimited  bool   `json:"is_business_limited"`
}

func toObserverErrorRequest(e *OpsErrorLog) *ObserverErrorRequest {
	if e == nil {
		return nil
	}
	return &ObserverErrorRequest{
		UserErrorRequest: *ToUserErrorRequest(e),
		Phase:            e.Phase, Type: e.Type, Severity: e.Severity, Owner: e.Owner, Source: e.Source, Resolved: e.Resolved,
		RequestID: e.RequestID, ClientRequestID: e.ClientRequestID, UserID: e.UserID, UserEmail: e.UserEmail,
		APIKeyID: e.APIKeyID, APIKeyName: e.APIKeyName, APIKeyDeleted: e.APIKeyDeleted,
		AccountID: e.AccountID, AccountName: e.AccountName, GroupID: e.GroupID, RequestPath: e.RequestPath,
		UpstreamEndpoint: e.UpstreamEndpoint, RequestedModel: e.RequestedModel, UpstreamModel: e.UpstreamModel,
	}
}

type ObserverErrorRequestList struct {
	Items    []*ObserverErrorRequest
	Total    int
	Page     int
	PageSize int
}

func (s *OpsService) ListObserverErrorRequests(ctx context.Context, userID int64, filter *OpsErrorLogFilter) (*ObserverErrorRequestList, error) {
	// Start from a whitelist instead of inheriting admin-only filters.
	f := &OpsErrorLogFilter{UserID: &userID, View: "all", ExcludeCountTokens: true}
	if filter != nil {
		f.StartTime, f.EndTime = filter.StartTime, filter.EndTime
		f.APIKeyID, f.AccountID, f.GroupID = filter.APIKeyID, filter.AccountID, filter.GroupID
		f.Model, f.Phase, f.StatusCodes = filter.Model, filter.Phase, filter.StatusCodes
		f.ErrorPhasesAny, f.ErrorTypesAny = filter.ErrorPhasesAny, filter.ErrorTypesAny
		f.Page, f.PageSize, f.SortBy, f.SortOrder = filter.Page, filter.PageSize, filter.SortBy, filter.SortOrder
	}
	list, err := s.opsRepo.ListErrorLogs(ctx, f)
	if err != nil {
		return nil, err
	}
	items := make([]*ObserverErrorRequest, 0, len(list.Errors))
	for _, row := range list.Errors {
		if row != nil && row.UserID != nil && *row.UserID == userID {
			items = append(items, toObserverErrorRequest(row))
		}
	}
	return &ObserverErrorRequestList{Items: items, Total: list.Total, Page: list.Page, PageSize: list.PageSize}, nil
}

func (s *OpsService) GetObserverErrorRequestDetail(ctx context.Context, userID, id int64) (*ObserverErrorRequestDetail, error) {
	detail, err := s.getOwnedErrorRequestDetail(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	return &ObserverErrorRequestDetail{ObserverErrorRequest: *toObserverErrorRequest(&detail.OpsErrorLog), ErrorBody: detail.ErrorBody, UpstreamStatusCode: detail.UpstreamStatusCode, IsBusinessLimited: detail.IsBusinessLimited}, nil
}
