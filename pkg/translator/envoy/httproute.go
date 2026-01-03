package envoy

import (
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	matcherv3 "github.com/envoyproxy/go-control-plane/envoy/type/matcher/v3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/klog/v2"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	aigatewayv0alpha0 "sigs.k8s.io/wg-ai-gateway/api/v0alpha0"
	aigatewaylisters "sigs.k8s.io/wg-ai-gateway/k8s/client/listers/api/v0alpha0"
)

func translateHTTPRouteToEnvoyRoutes(
	httpRoute *gatewayv1.HTTPRoute,
	serviceLister corev1listers.ServiceLister,
	backendLister aigatewaylisters.BackendLister,
) ([]*routev3.Route, []*aigatewayv0alpha0.Backend, metav1.Condition) {
	var envoyRoutes []*routev3.Route
	var allValidBackends []*aigatewayv0alpha0.Backend
	overallCondition := createSuccessCondition(httpRoute.Generation)

	for _, rule := range httpRoute.Spec.Rules {
		// These are the different operations that an HTTPRoute rule can specify
		var redirectAction *routev3.RedirectAction
		var requestHeadersToAdd []*corev3.HeaderValueOption
		var requestHeadersToRemove []string
		var responseHeadersToAdd []*corev3.HeaderValueOption
		var responseHeadersToRemove []string
		var urlRewriteAction *routev3.RouteAction

		for _, filter := range rule.Filters {
			switch filter.Type {
			// N.B if, for whatever reason, there are multiple redirect filters, only the last one will take effect
			case gatewayv1.HTTPRouteFilterRequestRedirect:
				redirectAction = translateRequestRedirectFilter(filter.RequestRedirect)
			case gatewayv1.HTTPRouteFilterURLRewrite:
				urlRewriteAction = translateURLRewriteFilter(filter.URLRewrite)
			case gatewayv1.HTTPRouteFilterRequestHeaderModifier:
				add, remove := translateRequestHeaderModifierFilter(filter.RequestHeaderModifier)
				requestHeadersToAdd = append(requestHeadersToAdd, add...)
				requestHeadersToRemove = append(requestHeadersToRemove, remove...)
			case gatewayv1.HTTPRouteFilterResponseHeaderModifier:
				add, remove := translateResponseHeaderModifierFilter(filter.ResponseHeaderModifier)
				responseHeadersToAdd = append(responseHeadersToAdd, add...)
				responseHeadersToRemove = append(responseHeadersToRemove, remove...)
			case gatewayv1.HTTPRouteFilterExtensionRef:
				// Extension filters are implementation-specific and would need custom handling
				klog.Infof("ExtensionRef filter not implemented: %v", filter.ExtensionRef)
			default:
				// Unsupported filter type; skip
				klog.Warningf("Unsupported HTTPRoute filter type: %s", filter.Type)
			}
		}
	}
}

func translateRequestRedirectFilter(requestRedirect *gatewayv1.HTTPRequestRedirectFilter) *routev3.RedirectAction {
	if requestRedirect == nil {
		return nil
	}

	redirectAction := &routev3.RedirectAction{}

	// Handle scheme redirect
	if requestRedirect.Scheme != nil {
		redirectAction.SchemeRewriteSpecifier = &routev3.RedirectAction_SchemeRedirect{
			SchemeRedirect: *requestRedirect.Scheme,
		}
	}

	// Handle hostname redirect
	if requestRedirect.Hostname != nil {
		redirectAction.HostRedirect = string(*requestRedirect.Hostname)
	}

	// Handle path redirect
	if requestRedirect.Path != nil {
		switch requestRedirect.Path.Type {
		case gatewayv1.FullPathHTTPPathModifier:
			redirectAction.PathRewriteSpecifier = &routev3.RedirectAction_PathRedirect{
				PathRedirect: *requestRedirect.Path.ReplaceFullPath,
			}
		case gatewayv1.PrefixMatchHTTPPathModifier:
			redirectAction.PathRewriteSpecifier = &routev3.RedirectAction_PrefixRewrite{
				PrefixRewrite: *requestRedirect.Path.ReplacePrefixMatch,
			}
		}
	}

	// Handle port redirect
	if requestRedirect.Port != nil {
		redirectAction.PortRedirect = uint32(*requestRedirect.Port)
	}

	// Handle status code (default to 302 if not specified)
	if requestRedirect.StatusCode != nil {
		redirectAction.ResponseCode = routev3.RedirectAction_RedirectResponseCode(*requestRedirect.StatusCode)
	} else {
		redirectAction.ResponseCode = routev3.RedirectAction_FOUND // 302
	}

	return redirectAction
}

func translateURLRewriteFilter(urlRewrite *gatewayv1.HTTPURLRewriteFilter) *routev3.RouteAction {
	if urlRewrite == nil {
		return nil
	}

	routeAction := &routev3.RouteAction{}
	// The flag prevents the function from returning an empty &routev3.RouteAction{}
	// struct when no actual rewrite is needed.
	rewriteActionSet := false

	// Handle hostname rewrite
	if urlRewrite.Hostname != nil {
		routeAction.HostRewriteSpecifier = &routev3.RouteAction_HostRewriteLiteral{
			HostRewriteLiteral: string(*urlRewrite.Hostname),
		}
	}

	// Handle path rewrite
	if urlRewrite.Path != nil {
		switch urlRewrite.Path.Type {
		case gatewayv1.FullPathHTTPPathModifier:
			if urlRewrite.Path.ReplaceFullPath != nil {
				routeAction.RegexRewrite = &matcherv3.RegexMatchAndSubstitute{
					Pattern:      &matcherv3.RegexMatcher{EngineType: &matcherv3.RegexMatcher_GoogleRe2{}, Regex: ".*"},
					Substitution: *urlRewrite.Path.ReplaceFullPath,
				}
				rewriteActionSet = true
			}
		case gatewayv1.PrefixMatchHTTPPathModifier:
			if urlRewrite.Path.ReplacePrefixMatch != nil {
				routeAction.PrefixRewrite = *urlRewrite.Path.ReplacePrefixMatch
				rewriteActionSet = true
			}
		}
	}

	// If no rewrite actions were set, return nil.
	if !rewriteActionSet {
		return nil
	}

	return routeAction
}

func translateRequestHeaderModifierFilter(headerModifier *gatewayv1.HTTPHeaderFilter) ([]*corev3.HeaderValueOption, []string) {
	if headerModifier == nil {
		return nil, nil
	}

	var headersToAdd []*corev3.HeaderValueOption
	var headersToRemove []string

	// Handle headers to set/add
	if headerModifier.Set != nil {
		for _, header := range headerModifier.Set {
			headersToAdd = append(headersToAdd, &corev3.HeaderValueOption{
				Header: &corev3.HeaderValue{
					Key:   string(header.Name),
					Value: header.Value,
				},
				AppendAction: corev3.HeaderValueOption_OVERWRITE_IF_EXISTS_OR_ADD,
			})
		}
	}

	if headerModifier.Add != nil {
		for _, header := range headerModifier.Add {
			headersToAdd = append(headersToAdd, &corev3.HeaderValueOption{
				Header: &corev3.HeaderValue{
					Key:   string(header.Name),
					Value: header.Value,
				},
				AppendAction: corev3.HeaderValueOption_APPEND_IF_EXISTS_OR_ADD,
			})
		}
	}

	// Handle headers to remove
	if headerModifier.Remove != nil {
		headersToRemove = append(headersToRemove, headerModifier.Remove...)
	}

	return headersToAdd, headersToRemove
}

func translateResponseHeaderModifierFilter(headerModifier *gatewayv1.HTTPHeaderFilter) ([]*corev3.HeaderValueOption, []string) {
	if headerModifier == nil {
		return nil, nil
	}

	var headersToAdd []*corev3.HeaderValueOption
	var headersToRemove []string

	// Handle headers to set/add (same logic as request headers)
	if headerModifier.Set != nil {
		for _, header := range headerModifier.Set {
			headersToAdd = append(headersToAdd, &corev3.HeaderValueOption{
				Header: &corev3.HeaderValue{
					Key:   string(header.Name),
					Value: header.Value,
				},
				AppendAction: corev3.HeaderValueOption_OVERWRITE_IF_EXISTS_OR_ADD,
			})
		}
	}

	if headerModifier.Add != nil {
		for _, header := range headerModifier.Add {
			headersToAdd = append(headersToAdd, &corev3.HeaderValueOption{
				Header: &corev3.HeaderValue{
					Key:   string(header.Name),
					Value: header.Value,
				},
				AppendAction: corev3.HeaderValueOption_APPEND_IF_EXISTS_OR_ADD,
			})
		}
	}

	// Handle headers to remove
	if headerModifier.Remove != nil {
		headersToRemove = append(headersToRemove, headerModifier.Remove...)
	}

	return headersToAdd, headersToRemove
}

func createSuccessCondition(generation int64) metav1.Condition {
	return metav1.Condition{
		Type:               string(gatewayv1.RouteConditionResolvedRefs),
		Status:             metav1.ConditionTrue,
		Reason:             string(gatewayv1.RouteReasonResolvedRefs),
		Message:            "All references resolved",
		ObservedGeneration: generation,
		LastTransitionTime: metav1.Now(),
	}
}
