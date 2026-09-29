package contract

import (
	dash "github.com/xraph/forge/extensions/dashboard/contract"
)

// scope is the app a request may see and change. The app is the security
// boundary: a tenant is the customer being billed, and an operator sees every
// customer in their app.
type scope struct {
	AppID string
}

// owns reports whether an entity belongs to this scope's app.
func (s scope) owns(entityApp string) bool {
	return entityApp == s.AppID
}

// canRead additionally allows global entities (empty app id), which the
// feature catalog uses for features shared by every app.
func (s scope) canRead(entityApp string) bool {
	return entityApp == s.AppID || entityApp == ""
}

// resolveScope takes the app from the principal's app_id claim, falling back to
// the configured app. The claim is set server-side by the dashboard's app
// switcher; nothing in a request can choose the app.
func resolveScope(p dash.Principal, deps Deps) (scope, error) {
	if claim, ok := p.Claims["app_id"].(string); ok && claim != "" {
		return scope{AppID: claim}, nil
	}

	if deps.RequireAppClaim {
		return scope{}, &dash.Error{
			Code:    dash.CodePermissionDenied,
			Message: "no app selected: this deployment requires an app_id claim",
		}
	}

	return scope{AppID: deps.AppID}, nil
}
