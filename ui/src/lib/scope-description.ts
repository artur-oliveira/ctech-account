import type { TFunction } from 'i18next'

/**
 * Renders a human-readable description for a scope. OIDC scopes have fixed
 * translations; service scopes (service:resource:action) are described
 * structurally from their segments. Descriptions come from the IdP side on
 * purpose — a client-supplied description could lie about what a scope does.
 */
export function describeScope(scope: string, t: TFunction): string {
  const oidcKey = `scopeDescriptions.${scope}`
  if (['openid', 'profile', 'email'].includes(scope)) return t(oidcKey)

  const [service, resource, action] = scope.split(':')
  if (!resource) return scope
  return t('scopeDescriptions.service', {
    service,
    resource: resource === '*' ? t('scopeDescriptions.allResources') : resource,
    action: !action || action === '*' ? t('scopeDescriptions.allActions') : action,
  })
}
