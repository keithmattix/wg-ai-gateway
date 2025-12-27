package envoy

import (
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1listers "k8s.io/client-go/listers/core/v1"
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
		// These are the differnet operations that an HTTPRoute rule can specify
		var redirectAction *routev3.RedirectAction
		var headersToAdd []*corev3.HeaderValueOption
		var headersToRemove []string
		var urlRewriteAction *routev3.RouteAction

		for _, filter := range rule.Filters {
			switch filter.Type {
			// N.B if, for whatever reason, there are multiple redirect filters, only the last one will take effect
			case gatewayv1.HTTPRouteFilterRequestRedirect:
				redirectAction = translateRequestRedirectFilter(filter.RequestRedirect)
			}
		}
	}
}

func translateRequestRedirectFilter(requestRedirect *gatewayv1.HTTPRequestRedirectFilter) *routev3.RedirectAction {

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
