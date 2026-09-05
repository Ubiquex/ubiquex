# UBI-175 Phase 1: Live corpus audit

Real, itemized findings from a static-analysis-only sweep of the live
`ubiquex-docs` resource-reference corpus (4,665 .mdx files, 4,207 real
resource pages + 457 index pages, across aws/azure/gcp/datadog/github/
kubernetes). No per-page browser instances used -- `mint validate`,
`mint broken-links`, static text/JSON analysis, plus one `mint dev`
instance reused for a 12-page rendered spot-check via curl (not a
browser). This is the checklist; the rebuild has to clear every item
below.

## Summary

| Class | Result | Count |
|---|---|---|
| 1. Pages that fail to load / structurally broken | **EMPTY** (verified) | 0 |
| 2. Category = single generic word | **REAL, corpus-wide** | 49 severe categories (153 pages) + 35 truncated categories (184 pages) |
| 3. Intro merely restates/says nothing | **REAL, dominant** | 4,200 / 4,207 pages (99.83%) |
| 4. Three-line AI-inferred boilerplate | **EMPTY** (already fixed today) | 0 |
| Code examples that are fragments | **EMPTY** (verified) | 0 |
| Em dashes | **EMPTY** in corpus | 0 (5 exist outside resource-reference/, out of scope) |
| Provenance claim now wrong | **EMPTY** (verified exhaustively) | 0 |
| Duplicate pages | **EMPTY** | 0 |
| Orphaned pages (new finding) | **REAL** | 449 index.mdx pages unreachable from nav |
| Broken links (new finding, separated from tool false positives) | **REAL** | 20 occurrences / 9 files |

**The single largest, most consequential finding is Class 3.** It isn't
just "restates the name" -- 99.83% of the corpus carries an identical
templated sentence describing the SDK generation pipeline itself, not
the resource. Only 7 pages in the whole corpus (all AWS) have real,
resource-specific intro content.

---

## Class 2 detail: category = single generic/truncated word

### SEVERE -- token has no product-identifying value at all, or conflates 2+ unrelated products

- **[AWS] "Default"** (6 page(s)):
  - resource-reference/aws/default/network-acl.mdx
  - resource-reference/aws/default/route-table.mdx
  - resource-reference/aws/default/security-group.mdx
  - resource-reference/aws/default/subnet.mdx
  - resource-reference/aws/default/vpc.mdx
  - resource-reference/aws/default/vpc-dhcp-options.mdx
- **[AWS] "Egress"** (1 page(s)):
  - resource-reference/aws/egress/only-internet-gateway.mdx
- **[AWS] "Flow"** (1 page(s)):
  - resource-reference/aws/flow/log.mdx
- **[AWS] "Instance"** (1 page(s)):
  - resource-reference/aws/instance/instance.mdx
- **[AWS] "Internet"** (2 page(s)):
  - resource-reference/aws/internet/gateway.mdx
  - resource-reference/aws/internet/gateway-attachment.mdx
- **[AWS] "Key"** (1 page(s)):
  - resource-reference/aws/key/pair.mdx
- **[AWS] "Launch"** (2 page(s)):
  - resource-reference/aws/launch/configuration.mdx
  - resource-reference/aws/launch/template.mdx
- **[AWS] "Load"** (3 page(s)):
  - resource-reference/aws/load/balancer-backend-server-policy.mdx
  - resource-reference/aws/load/balancer-listener-policy.mdx
  - resource-reference/aws/load/balancer-policy.mdx
- **[AWS] "Main"** (1 page(s)):
  - resource-reference/aws/main/route-table-association.mdx
- **[AWS] "Placement"** (1 page(s)):
  - resource-reference/aws/placement/group.mdx
- **[AWS] "Proxy"** (1 page(s)):
  - resource-reference/aws/proxy/protocol-policy.mdx
- **[AWS] "Security"** (2 page(s)):
  - resource-reference/aws/security/group.mdx
  - resource-reference/aws/security/group-rule.mdx
- **[AWS] "Service"** (5 page(s)):
  - resource-reference/aws/service/discovery-http-namespace.mdx
  - resource-reference/aws/service/discovery-instance.mdx
  - resource-reference/aws/service/discovery-private-dns-namespace.mdx
  - resource-reference/aws/service/discovery-public-dns-namespace.mdx
  - resource-reference/aws/service/discovery-service.mdx
- **[AWS] "Snapshot"** (1 page(s)):
  - resource-reference/aws/snapshot/create-volume-permission.mdx
- **[AWS] "Spot"** (3 page(s)):
  - resource-reference/aws/spot/datafeed-subscription.mdx
  - resource-reference/aws/spot/fleet-request.mdx
  - resource-reference/aws/spot/instance-request.mdx
- **[AWS] "Subnet"** (1 page(s)):
  - resource-reference/aws/subnet/subnet.mdx
- **[AWS] "Volume"** (1 page(s)):
  - resource-reference/aws/volume/attachment.mdx
- **[Azure] "Advanced"** (1 page(s)):
  - resource-reference/azure/advanced/threat-protection.mdx
- **[Azure] "Custom"** (2 page(s)):
  - resource-reference/azure/custom/ip-prefix.mdx
  - resource-reference/azure/custom/provider.mdx
- **[Azure] "Dedicated"** (3 page(s)):
  - resource-reference/azure/dedicated/hardware-security-module.mdx
  - resource-reference/azure/dedicated/host.mdx
  - resource-reference/azure/dedicated/host-group.mdx
- **[Azure] "Extended"** (1 page(s)):
  - resource-reference/azure/extended/location-custom-location.mdx
- **[Azure] "Federated"** (1 page(s)):
  - resource-reference/azure/federated/identity-credential.mdx
- **[Azure] "Local"** (1 page(s)):
  - resource-reference/azure/local/network-gateway.mdx
- **[Azure] "Managed"** (9 page(s)):
  - resource-reference/azure/managed/application.mdx
  - resource-reference/azure/managed/application-definition.mdx
  - resource-reference/azure/managed/devops-pool.mdx
  - resource-reference/azure/managed/disk.mdx
  - resource-reference/azure/managed/disk-sas-token.mdx
  - resource-reference/azure/managed/lustre-file-system.mdx
  - resource-reference/azure/managed/redis.mdx
  - resource-reference/azure/managed/redis-access-policy-assignment.mdx
  - resource-reference/azure/managed/redis-geo-replication.mdx
- **[Azure] "New"** (2 page(s)):
  - resource-reference/azure/new/relic-monitor.mdx
  - resource-reference/azure/new/relic-tag-rule.mdx
- **[Azure] "Orchestrated"** (1 page(s)):
  - resource-reference/azure/orchestrated/virtual-machine-scale-set.mdx
- **[Azure] "Point"** (1 page(s)):
  - resource-reference/azure/point/to-site-vpn-gateway.mdx
- **[Azure] "Private"** (18 page(s)):
  - resource-reference/azure/private/dns-a-record.mdx
  - resource-reference/azure/private/dns-aaaa-record.mdx
  - resource-reference/azure/private/dns-cname-record.mdx
  - resource-reference/azure/private/dns-mx-record.mdx
  - resource-reference/azure/private/dns-ptr-record.mdx
  - resource-reference/azure/private/dns-resolver.mdx
  - resource-reference/azure/private/dns-resolver-dns-forwarding-ruleset.mdx
  - resource-reference/azure/private/dns-resolver-forwarding-rule.mdx
  - resource-reference/azure/private/dns-resolver-inbound-endpoint.mdx
  - resource-reference/azure/private/dns-resolver-outbound-endpoint.mdx
  - resource-reference/azure/private/dns-resolver-virtual-network-link.mdx
  - resource-reference/azure/private/dns-srv-record.mdx
  - resource-reference/azure/private/dns-txt-record.mdx
  - resource-reference/azure/private/dns-zone.mdx
  - resource-reference/azure/private/dns-zone-virtual-network-link.mdx
  - resource-reference/azure/private/endpoint.mdx
  - resource-reference/azure/private/endpoint-application-security-group-association.mdx
  - resource-reference/azure/private/link-service.mdx
- **[Azure] "Proximity"** (1 page(s)):
  - resource-reference/azure/proximity/placement-group.mdx
- **[Azure] "Public"** (2 page(s)):
  - resource-reference/azure/public/ip.mdx
  - resource-reference/azure/public/ip-prefix.mdx
- **[Azure] "Resource"** (16 page(s)):
  - resource-reference/azure/resource/deployment-script-azure-cli.mdx
  - resource-reference/azure/resource/deployment-script-azure-power-shell.mdx
  - resource-reference/azure/resource/group.mdx
  - resource-reference/azure/resource/group-cost-management-export.mdx
  - resource-reference/azure/resource/group-cost-management-view.mdx
  - resource-reference/azure/resource/group-policy-assignment.mdx
  - resource-reference/azure/resource/group-policy-exemption.mdx
  - resource-reference/azure/resource/group-policy-remediation.mdx
  - resource-reference/azure/resource/group-template-deployment.mdx
  - resource-reference/azure/resource/management-private-link.mdx
  - resource-reference/azure/resource/management-private-link-association.mdx
  - resource-reference/azure/resource/policy-assignment.mdx
  - resource-reference/azure/resource/policy-exemption.mdx
  - resource-reference/azure/resource/policy-remediation.mdx
  - resource-reference/azure/resource/provider-feature-registration.mdx
  - resource-reference/azure/resource/provider-registration.mdx
- **[Azure] "Role"** (3 page(s)):
  - resource-reference/azure/role/assignment.mdx
  - resource-reference/azure/role/definition.mdx
  - resource-reference/azure/role/management-policy.mdx
- **[Azure] "Service"** (3 page(s)):
  - resource-reference/azure/service/fabric-cluster.mdx
  - resource-reference/azure/service/fabric-managed-cluster.mdx
  - resource-reference/azure/service/plan.mdx
- **[Azure] "Source"** (1 page(s)):
  - resource-reference/azure/source/control-token.mdx
- **[Azure] "Trusted"** (1 page(s)):
  - resource-reference/azure/trusted/signing-account.mdx
- **[Azure] "User"** (1 page(s)):
  - resource-reference/azure/user/assigned-identity.mdx
- **[Azure] "Web"** (10 page(s)):
  - resource-reference/azure/web/app-active-slot.mdx
  - resource-reference/azure/web/app-hybrid-connection.mdx
  - resource-reference/azure/web/application-firewall-policy.mdx
  - resource-reference/azure/web/pubsub.mdx
  - resource-reference/azure/web/pubsub-custom-certificate.mdx
  - resource-reference/azure/web/pubsub-custom-domain.mdx
  - resource-reference/azure/web/pubsub-hub.mdx
  - resource-reference/azure/web/pubsub-network-acl.mdx
  - resource-reference/azure/web/pubsub-shared-private-link-resource.mdx
  - resource-reference/azure/web/pubsub-socketio.mdx
- **[Datadog] "Shared"** (1 page(s)):
  - resource-reference/datadog/shared/dashboard.mdx
- **[Datadog] "User"** (1 page(s)):
  - resource-reference/datadog/user/response.mdx
- **[GCP] "Config"** (1 page(s)):
  - resource-reference/gcp/config/deployment.mdx
- **[GCP] "Container"** (14 page(s)):
  - resource-reference/gcp/container/analysis-note.mdx
  - resource-reference/gcp/container/analysis-note-iam-binding.mdx
  - resource-reference/gcp/container/analysis-note-iam-member.mdx
  - resource-reference/gcp/container/analysis-note-iam-policy.mdx
  - resource-reference/gcp/container/analysis-occurrence.mdx
  - resource-reference/gcp/container/attached-cluster.mdx
  - resource-reference/gcp/container/aws-cluster.mdx
  - resource-reference/gcp/container/aws-node-pool.mdx
  - resource-reference/gcp/container/azure-client.mdx
  - resource-reference/gcp/container/azure-cluster.mdx
  - resource-reference/gcp/container/azure-node-pool.mdx
  - resource-reference/gcp/container/cluster.mdx
  - resource-reference/gcp/container/node-pool.mdx
  - resource-reference/gcp/container/registry.mdx
- **[GCP] "Model"** (2 page(s)):
  - resource-reference/gcp/model/armor-floorsetting.mdx
  - resource-reference/gcp/model/armor-template.mdx
- **[GCP] "Service"** (17 page(s)):
  - resource-reference/gcp/service/account.mdx
  - resource-reference/gcp/service/account-iam-binding.mdx
  - resource-reference/gcp/service/account-iam-member.mdx
  - resource-reference/gcp/service/account-iam-policy.mdx
  - resource-reference/gcp/service/account-key.mdx
  - resource-reference/gcp/service/directory-endpoint.mdx
  - resource-reference/gcp/service/directory-namespace.mdx
  - resource-reference/gcp/service/directory-namespace-iam-binding.mdx
  - resource-reference/gcp/service/directory-namespace-iam-member.mdx
  - resource-reference/gcp/service/directory-namespace-iam-policy.mdx
  - resource-reference/gcp/service/directory-service.mdx
  - resource-reference/gcp/service/directory-service-iam-binding.mdx
  - resource-reference/gcp/service/directory-service-iam-member.mdx
  - resource-reference/gcp/service/directory-service-iam-policy.mdx
  - resource-reference/gcp/service/networking-connection.mdx
  - resource-reference/gcp/service/networking-peered-dns-domain.mdx
  - resource-reference/gcp/service/networking-vpc-service-controls.mdx
- **[GitHub] "Custom"** (1 page(s)):
  - resource-reference/github/custom/property.mdx
- **[GitHub] "Full"** (1 page(s)):
  - resource-reference/github/full/repository.mdx
- **[GitHub] "Get"** (1 page(s)):
  - resource-reference/github/get/budget.mdx
- **[GitHub] "Key"** (1 page(s)):
  - resource-reference/github/key/key.mdx
- **[GitHub] "Simple"** (1 page(s)):
  - resource-reference/github/simple/user.mdx
- **[GitHub] "Stack"** (1 page(s)):
  - resource-reference/github/stack/stack.mdx

SEVERE total: 49 categories, 153 pages

### TRUNCATED -- token is the start of a real product name but cut to one word, real defect but milder

- **[Azure] "Active"** (3 page(s)):
  - resource-reference/azure/active/directory-domain-service.mdx
  - resource-reference/azure/active/directory-domain-service-replica-set.mdx
  - resource-reference/azure/active/directory-domain-service-trust.mdx
- **[Azure] "Confidential"** (1 page(s)):
  - resource-reference/azure/confidential/ledger.mdx
- **[Azure] "Database"** (2 page(s)):
  - resource-reference/azure/database/migration-project.mdx
  - resource-reference/azure/database/migration-service.mdx
- **[Azure] "Digital"** (5 page(s)):
  - resource-reference/azure/digital/twins-endpoint-eventgrid.mdx
  - resource-reference/azure/digital/twins-endpoint-eventhub.mdx
  - resource-reference/azure/digital/twins-endpoint-servicebus.mdx
  - resource-reference/azure/digital/twins-instance.mdx
  - resource-reference/azure/digital/twins-time-series-database-connection.mdx
- **[Azure] "Key"** (14 page(s)):
  - resource-reference/azure/key/vault.mdx
  - resource-reference/azure/key/vault-access-policy.mdx
  - resource-reference/azure/key/vault-certificate.mdx
  - resource-reference/azure/key/vault-certificate-contacts.mdx
  - resource-reference/azure/key/vault-certificate-issuer.mdx
  - resource-reference/azure/key/vault-key.mdx
  - resource-reference/azure/key/vault-managed-hardware-security-module.mdx
  - resource-reference/azure/key/vault-managed-hardware-security-module-key.mdx
  - resource-reference/azure/key/vault-managed-hardware-security-module-key-rotation-policy.mdx
  - resource-reference/azure/key/vault-managed-hardware-security-module-role-assignment.mdx
  - resource-reference/azure/key/vault-managed-hardware-security-module-role-definition.mdx
  - resource-reference/azure/key/vault-managed-storage-account.mdx
  - resource-reference/azure/key/vault-managed-storage-account-sas-token-definition.mdx
  - resource-reference/azure/key/vault-secret.mdx
- **[Azure] "Load"** (1 page(s)):
  - resource-reference/azure/load/test.mdx
- **[Azure] "Log"** (16 page(s)):
  - resource-reference/azure/log/analytics-cluster.mdx
  - resource-reference/azure/log/analytics-cluster-customer-managed-key.mdx
  - resource-reference/azure/log/analytics-data-export-rule.mdx
  - resource-reference/azure/log/analytics-datasource-windows-event.mdx
  - resource-reference/azure/log/analytics-datasource-windows-performance-counter.mdx
  - resource-reference/azure/log/analytics-linked-service.mdx
  - resource-reference/azure/log/analytics-linked-storage-account.mdx
  - resource-reference/azure/log/analytics-query-pack.mdx
  - resource-reference/azure/log/analytics-query-pack-query.mdx
  - resource-reference/azure/log/analytics-saved-search.mdx
  - resource-reference/azure/log/analytics-solution.mdx
  - resource-reference/azure/log/analytics-storage-insights.mdx
  - resource-reference/azure/log/analytics-workspace.mdx
  - resource-reference/azure/log/analytics-workspace-table.mdx
  - resource-reference/azure/log/analytics-workspace-table-custom-log.mdx
  - resource-reference/azure/log/analytics-workspace-table-microsoft.mdx
- **[Azure] "Management"** (8 page(s)):
  - resource-reference/azure/management/group.mdx
  - resource-reference/azure/management/group-policy-assignment.mdx
  - resource-reference/azure/management/group-policy-exemption.mdx
  - resource-reference/azure/management/group-policy-remediation.mdx
  - resource-reference/azure/management/group-policy-set-definition.mdx
  - resource-reference/azure/management/group-subscription-association.mdx
  - resource-reference/azure/management/group-template-deployment.mdx
  - resource-reference/azure/management/lock.mdx
- **[Azure] "Recovery"** (2 page(s)):
  - resource-reference/azure/recovery/services-vault.mdx
  - resource-reference/azure/recovery/services-vault-resource-guard-association.mdx
- **[Azure] "Search"** (2 page(s)):
  - resource-reference/azure/search/service.mdx
  - resource-reference/azure/search/shared-private-link-service.mdx
- **[Azure] "Security"** (10 page(s)):
  - resource-reference/azure/security/center-assessment.mdx
  - resource-reference/azure/security/center-assessment-policy.mdx
  - resource-reference/azure/security/center-automation.mdx
  - resource-reference/azure/security/center-contact.mdx
  - resource-reference/azure/security/center-server-vulnerability-assessment-virtual-machine.mdx
  - resource-reference/azure/security/center-server-vulnerability-assessments-setting.mdx
  - resource-reference/azure/security/center-setting.mdx
  - resource-reference/azure/security/center-storage-defender.mdx
  - resource-reference/azure/security/center-subscription-pricing.mdx
  - resource-reference/azure/security/center-workspace.mdx
- **[Azure] "Shared"** (3 page(s)):
  - resource-reference/azure/shared/image.mdx
  - resource-reference/azure/shared/image-gallery.mdx
  - resource-reference/azure/shared/image-version.mdx
- **[Azure] "Site"** (14 page(s)):
  - resource-reference/azure/site/recovery-fabric.mdx
  - resource-reference/azure/site/recovery-hyperv-network-mapping.mdx
  - resource-reference/azure/site/recovery-hyperv-replication-policy.mdx
  - resource-reference/azure/site/recovery-hyperv-replication-policy-association.mdx
  - resource-reference/azure/site/recovery-network-mapping.mdx
  - resource-reference/azure/site/recovery-protection-container.mdx
  - resource-reference/azure/site/recovery-protection-container-mapping.mdx
  - resource-reference/azure/site/recovery-replicated-vm.mdx
  - resource-reference/azure/site/recovery-replication-policy.mdx
  - resource-reference/azure/site/recovery-replication-recovery-plan.mdx
  - resource-reference/azure/site/recovery-services-vault-hyperv-site.mdx
  - resource-reference/azure/site/recovery-vmware-replicated-vm.mdx
  - resource-reference/azure/site/recovery-vmware-replication-policy.mdx
  - resource-reference/azure/site/recovery-vmware-replication-policy-association.mdx
- **[Azure] "Snapshot"** (1 page(s)):
  - resource-reference/azure/snapshot/snapshot.mdx
- **[Azure] "Stack"** (8 page(s)):
  - resource-reference/azure/stack/hci-cluster.mdx
  - resource-reference/azure/stack/hci-deployment-setting.mdx
  - resource-reference/azure/stack/hci-extension.mdx
  - resource-reference/azure/stack/hci-logical-network.mdx
  - resource-reference/azure/stack/hci-marketplace-gallery-image.mdx
  - resource-reference/azure/stack/hci-network-interface.mdx
  - resource-reference/azure/stack/hci-storage-path.mdx
  - resource-reference/azure/stack/hci-virtual-hard-disk.mdx
- **[Azure] "Static"** (3 page(s)):
  - resource-reference/azure/static/web-app.mdx
  - resource-reference/azure/static/web-app-custom-domain.mdx
  - resource-reference/azure/static/web-app-function-app-registration.mdx
- **[Azure] "Subnet"** (5 page(s)):
  - resource-reference/azure/subnet/nat-gateway-association.mdx
  - resource-reference/azure/subnet/network-security-group-association.mdx
  - resource-reference/azure/subnet/route-table-association.mdx
  - resource-reference/azure/subnet/service-endpoint-storage-policy.mdx
  - resource-reference/azure/subnet/subnet.mdx
- **[Azure] "System"** (7 page(s)):
  - resource-reference/azure/system/center-virtual-machine-manager-availability-set.mdx
  - resource-reference/azure/system/center-virtual-machine-manager-cloud.mdx
  - resource-reference/azure/system/center-virtual-machine-manager-server.mdx
  - resource-reference/azure/system/center-virtual-machine-manager-virtual-machine-instance.mdx
  - resource-reference/azure/system/center-virtual-machine-manager-virtual-machine-instance-guest-agent.mdx
  - resource-reference/azure/system/center-virtual-machine-manager-virtual-machine-template.mdx
  - resource-reference/azure/system/center-virtual-machine-manager-virtual-network.mdx
- **[Azure] "Video"** (1 page(s)):
  - resource-reference/azure/video/indexer-account.mdx
- **[GCP] "Access"** (19 page(s)):
  - resource-reference/gcp/access/context-manager-access-level.mdx
  - resource-reference/gcp/access/context-manager-access-level-condition.mdx
  - resource-reference/gcp/access/context-manager-access-levels.mdx
  - resource-reference/gcp/access/context-manager-access-policy.mdx
  - resource-reference/gcp/access/context-manager-access-policy-iam-binding.mdx
  - resource-reference/gcp/access/context-manager-access-policy-iam-member.mdx
  - resource-reference/gcp/access/context-manager-access-policy-iam-policy.mdx
  - resource-reference/gcp/access/context-manager-authorized-orgs-desc.mdx
  - resource-reference/gcp/access/context-manager-egress-policy.mdx
  - resource-reference/gcp/access/context-manager-gcp-user-access-binding.mdx
  - resource-reference/gcp/access/context-manager-ingress-policy.mdx
  - resource-reference/gcp/access/context-manager-service-perimeter.mdx
  - resource-reference/gcp/access/context-manager-service-perimeter-dry-run-egress-policy.mdx
  - resource-reference/gcp/access/context-manager-service-perimeter-dry-run-ingress-policy.mdx
  - resource-reference/gcp/access/context-manager-service-perimeter-dry-run-resource.mdx
  - resource-reference/gcp/access/context-manager-service-perimeter-egress-policy.mdx
  - resource-reference/gcp/access/context-manager-service-perimeter-ingress-policy.mdx
  - resource-reference/gcp/access/context-manager-service-perimeter-resource.mdx
  - resource-reference/gcp/access/context-manager-service-perimeters.mdx
- **[GCP] "Active"** (2 page(s)):
  - resource-reference/gcp/active/directory-domain.mdx
  - resource-reference/gcp/active/directory-domain-trust.mdx
- **[GCP] "Assured"** (1 page(s)):
  - resource-reference/gcp/assured/workloads-workload.mdx
- **[GCP] "Binary"** (5 page(s)):
  - resource-reference/gcp/binary/authorization-attestor.mdx
  - resource-reference/gcp/binary/authorization-attestor-iam-binding.mdx
  - resource-reference/gcp/binary/authorization-attestor-iam-member.mdx
  - resource-reference/gcp/binary/authorization-attestor-iam-policy.mdx
  - resource-reference/gcp/binary/authorization-policy.mdx
- **[GCP] "Database"** (3 page(s)):
  - resource-reference/gcp/database/migration-service-connection-profile.mdx
  - resource-reference/gcp/database/migration-service-migration-job.mdx
  - resource-reference/gcp/database/migration-service-private-connection.mdx
- **[GCP] "Deployment"** (1 page(s)):
  - resource-reference/gcp/deployment/manager-deployment.mdx
- **[GCP] "Discovery"** (19 page(s)):
  - resource-reference/gcp/discovery/engine-acl-config.mdx
  - resource-reference/gcp/discovery/engine-assistant.mdx
  - resource-reference/gcp/discovery/engine-chat-engine.mdx
  - resource-reference/gcp/discovery/engine-cmek-config.mdx
  - resource-reference/gcp/discovery/engine-control.mdx
  - resource-reference/gcp/discovery/engine-data-connector.mdx
  - resource-reference/gcp/discovery/engine-data-store.mdx
  - resource-reference/gcp/discovery/engine-license-config.mdx
  - resource-reference/gcp/discovery/engine-recommendation-engine.mdx
  - resource-reference/gcp/discovery/engine-schema.mdx
  - resource-reference/gcp/discovery/engine-search-engine.mdx
  - resource-reference/gcp/discovery/engine-search-engine-iam-binding.mdx
  - resource-reference/gcp/discovery/engine-search-engine-iam-member.mdx
  - resource-reference/gcp/discovery/engine-search-engine-iam-policy.mdx
  - resource-reference/gcp/discovery/engine-serving-config.mdx
  - resource-reference/gcp/discovery/engine-sitemap.mdx
  - resource-reference/gcp/discovery/engine-target-site.mdx
  - resource-reference/gcp/discovery/engine-user-store.mdx
  - resource-reference/gcp/discovery/engine-widget-config.mdx
- **[GCP] "Document"** (5 page(s)):
  - resource-reference/gcp/document/ai-processor.mdx
  - resource-reference/gcp/document/ai-processor-default-version.mdx
  - resource-reference/gcp/document/ai-schema.mdx
  - resource-reference/gcp/document/ai-warehouse-document-schema.mdx
  - resource-reference/gcp/document/ai-warehouse-location.mdx
- **[GCP] "Essential"** (1 page(s)):
  - resource-reference/gcp/essential/contacts-contact.mdx
- **[GCP] "Identity"** (8 page(s)):
  - resource-reference/gcp/identity/platform-config.mdx
  - resource-reference/gcp/identity/platform-default-supported-idp-config.mdx
  - resource-reference/gcp/identity/platform-inbound-saml-config.mdx
  - resource-reference/gcp/identity/platform-oauth-idp-config.mdx
  - resource-reference/gcp/identity/platform-tenant.mdx
  - resource-reference/gcp/identity/platform-tenant-default-supported-idp-config.mdx
  - resource-reference/gcp/identity/platform-tenant-inbound-saml-config.mdx
  - resource-reference/gcp/identity/platform-tenant-oauth-idp-config.mdx
- **[GCP] "Integration"** (3 page(s)):
  - resource-reference/gcp/integration/connectors-connection.mdx
  - resource-reference/gcp/integration/connectors-endpoint-attachment.mdx
  - resource-reference/gcp/integration/connectors-managed-zone.mdx
- **[GCP] "License"** (1 page(s)):
  - resource-reference/gcp/license/manager-configuration.mdx
- **[GCP] "Managed"** (5 page(s)):
  - resource-reference/gcp/managed/kafka-acl.mdx
  - resource-reference/gcp/managed/kafka-cluster.mdx
  - resource-reference/gcp/managed/kafka-connect-cluster.mdx
  - resource-reference/gcp/managed/kafka-connector.mdx
  - resource-reference/gcp/managed/kafka-topic.mdx
- **[GCP] "Public"** (1 page(s)):
  - resource-reference/gcp/public/ca-external-account-key.mdx
- **[GCP] "Resource"** (2 page(s)):
  - resource-reference/gcp/resource/manager-capability.mdx
  - resource-reference/gcp/resource/manager-lien.mdx
- **[GCP] "Site"** (2 page(s)):
  - resource-reference/gcp/site/verification-owner.mdx
  - resource-reference/gcp/site/verification-web-resource.mdx

TRUNCATED total: 35 categories, 184 pages

### Reviewed and judged NOT a defect (real, correct category names despite being single words)

- [AWS] "Config" (13 pages) -- AWS Config is a real, correctly-named AWS service.
- [Azure] "Container" (21 pages) -- Coherent umbrella (Container Apps/Registry/Instances) matching Azure's own portal taxonomy.
- [Azure] "Policy" (3 pages) -- Azure Policy is the real product name itself.
- [Azure] "Portal" (2 pages) -- Reasonably identifying on its own (Azure Portal dashboards).
- [GCP] "Project" (12 pages) -- Project is a real, meaningful GCP resource-container concept, not a truncation.
- [GitHub] "Deployment" (4 pages) -- Deployment is a real, correct GitHub Deployments API concept.
- [Kubernetes] "Discovery" (1 pages) -- Matches the real discovery.k8s.io API group name.
- [Kubernetes] "Resource" (6 pages) -- Matches the real resource.k8s.io API group name (Dynamic Resource Allocation).

---

## Class 1: pages that fail to load or are structurally broken

**EMPTY. Verified, not assumed.**

- `mint validate`: clean, zero errors, across the whole site.
- `mint broken-links`: 54 raw hits in 39 files, but 34 hits (30 files) are false positives -- regex character-class fragments inside vendor field-description text (e.g. `([-a-z0-9]*[a-z0-9])?`) that happen to look like `[text](url)` MDX link syntax to the checker. See Class 6 below for the real 20 broken links this run also surfaced (a genuinely different, real defect).
- Frontmatter: 0/4665 files with a missing/malformed `---` block or missing `title`.
- JSX tag balance (Tabs/Tab/ResponseField/Expandable/Note/Warning/Info/Tip): 0/4665 files unbalanced.
- Empty/near-empty body: 0/4665.
- Rendered spot-check (mint dev, one local instance, 12 representative pages -- one per provider plus the worst-offender generic-category pages -- fetched via plain curl, no browser tab per page): all 12 returned HTTP 200 with full real page content, zero server-error markers.

---

## Class 2 summary: sidebar category is a single generic word (the split-on-first-token problem)

**Confirmed structural, corpus-wide: all 541 sidebar categories across every provider are mechanically the Titlecased first token of the underlying resource type name.** Most land on real, correct product names (S3, Lambda, IAM, VPC, Sagemaker, ...) by coincidence of a short type name -- those are fine. Two real sub-classes of defect, detailed above:
- SEVERE: 49 categories / 153 pages -- the token has zero product-identifying value (Full, Get, Simple, Stack, New [as in "New Relic"], Default, Managed, Dedicated, Web, Service x3 providers, ...), including several that conflate two or more unrelated real products under one meaningless label (Azure Dedicated = HSM + Host; Azure/GCP Service = 2-3 unrelated products each; Azure Web = App Service + WAF + PubSub).
- TRUNCATED: 35 categories / 184 pages -- the token IS the start of a real product name (Key -> Key Vault, Discovery -> Discovery Engine, Log -> Log Analytics) but cut to one word, losing the identifying part.
- 8 categories reviewed and judged correct despite being single words (real product names or real upstream API group names) -- listed explicitly above so this isn't asserted blindly.

---

## Class 3: intros that merely restate the resource name

**Confirmed, corpus-wide, and worse than the named description: the intro doesn't even restate the resource name -- it describes the GENERATION PIPELINE, not the resource.**

4,200 of 4,207 real resource pages (99.83%) open with a variant of the identical templated sentence:
  > "`{type}` -- real, typed bindings generated directly from {source}, in every SDK language. Every tab below is a complete, runnable program, not a fragment, real enough to save and run exactly as shown."

Zero resource-specific content: nothing about what the resource does, what it's for, or how its fields relate. Only 7 pages in the entire corpus have a genuine, resource-specific intro (all AWS, all pre-dating the templated pipeline):

- resource-reference/aws/s3/bucket.mdx
- resource-reference/aws/ecr/repository.mdx
- resource-reference/aws/sqs/queue.mdx
- resource-reference/aws/iam/policy.mdx
- resource-reference/aws/iam/role.mdx
- resource-reference/aws/iam/role-policy-attachment.mdx
- resource-reference/aws/vpc/vpc.mdx

Per-provider breakdown of pages still carrying the templated intro:
- aws: 1,891 / 1,898 non-index pages (7 exceptions above)
- azure: 1,103 / 1,103
- gcp: 1,257 / 1,257 (of the non-index resource pages)
- datadog: 25 / 25
- github: 68 / 68
- kubernetes: 71 / 71

---

## Class 4: pages still carrying the three-line AI-inferred boilerplate

**EMPTY. Already fixed corpus-wide, verified by direct text search, not assumed from the one commit that mentioned it.**

Old pattern (found via `git show 18bb8a0`, the fix commit from earlier today): a blank line plus
  > "**AI-inferred** -- not sourced from the real provider schema; generated by ubx's own description pipeline and never independently verified against the real provider."
repeated after every AI-inferred field. Zero occurrences of this string anywhere in the live corpus (checked all 6 providers individually, all zero). The current shape everywhere AI-inferred fields exist (GCP, Datadog, GitHub, Kubernetes -- AWS and Azure have none at all today) is the compact form: one page-level `<Note>` plus an inline **(AI-inferred)** suffix per field. This class is closed, not open.

---

## Code examples that are fragments rather than complete runnable programs

**EMPTY. Verified structurally across all 4,207 resource pages.**

- Every page has exactly the same 4 tabs: Go, TypeScript, Python, Markdown -- 0 pages with a different tab set.
- Every Go tab contains both `package main` and `func main(` -- 0 exceptions.
- Every TypeScript tab contains an `import` -- 0 exceptions.
- Every Python tab contains `import`/`from` -- 0 exceptions.
- 0 empty code blocks anywhere.
(An initial short-code-block heuristic flagged ~2,895 "Markdown"-tab blocks as suspiciously short; manual inspection showed these are legitimate, complete natural-language intent descriptions for resources with few required fields, e.g. "Create azurerm_advanced_threat_protection, with enabled true, target resource id example." -- not fragments. Noted so the false lead isn't rediscovered.)

---

## Em dashes

**EMPTY in the generated corpus (resource-reference/).** 0 occurrences of U+2014 in any of the 4,665 resource-reference .mdx files.
Not in scope for this phase, but found and worth a one-line mention: 5 em dashes exist elsewhere in the repo, in hand-authored docs, not the generated corpus:
- tutorial/promotion/promote.mdx
- tutorial/markdown/context.mdx
- concepts/context-aware-drafting.mdx
- concepts/conformance.mdx
- cli-reference/promote.mdx

---

## Pages whose provenance claim is now wrong

**EMPTY. Verified exhaustively, not spot-checked.**

Cross-checked every page's own provenance sentence against its real, known generation source:
- GCP: 95/95 compute/ pages correctly claim "Google Discovery Documents"; 1,161/1,161 non-compute pages correctly claim `hashicorp/google` -- 0 mismatches in either direction.
- Azure: 1,103/1,103 correctly claim `hashicorp/azurerm` -- 0 mismatches.
- AWS: 1,681/1,684 correctly claim `hashicorp/aws`; the 3 that don't (`iam/policy`, `iam/role`, `iam/role-policy-attachment`) are 3 of the 7 genuinely-custom-intro pages -- they don't name the source explicitly in their custom prose, but claim nothing false either. Minor completeness gap, not a wrong claim.
- Kubernetes/GitHub/Datadog: 100% correctly claim their own real OpenAPI-spec source, 0 mismatches.

---

## Duplicate or orphaned pages from the phased regeneration

**Duplicates: EMPTY.** 0 exact-duplicate-content page groups anywhere in the 4,665-file corpus (full SHA-256 content hash comparison).

**Orphaned: a real, previously-unnamed finding.** 449 of 457 per-category `index.mdx` landing pages are generated but never linked from `docs.json`'s own navigation at all -- each category group's `root` field points directly at that category's first real resource page instead of at its own `index.mdx`, so the index page is build output with no path to it from the live site. This is NOT caught by `mint validate` or `mint broken-links` (nothing points at them incorrectly -- they're just never pointed at). 8 index.mdx files ARE correctly referenced (the top-level `resource-reference/index.mdx` and each provider's own top-level index). Full list of the 449 in the companion file (one per almost every service subdirectory across all 6 providers).

---

## Class 6: real broken links (found via mint broken-links, separated from the tool's own false positives)

9 files, 20 link occurrences (11 unique targets). All GCP, all the same real bug: a vendor (HashiCorp `google` provider) field description contains a markdown link using a path relative to the VENDOR's own docs site (`cloud.google.com` or `registry.terraform.io`), which resolves as a root-relative, broken link on ubiquex-docs's own domain instead.

- **resource-reference/gcp/alloydb/user.mdx**
  - broken link target: `/docs/providers/google/guides/using_write_only_arguments.html#updating-write-only-arguments`
- **resource-reference/gcp/compute/autoscaler.mdx**
  - broken link target: `/compute/docs/autoscaler#cool_down_period`
  - broken link target: `/compute/docs/autoscaler#stabilization_period`
- **resource-reference/gcp/compute/region-autoscaler.mdx**
  - broken link target: `/compute/docs/autoscaler#cool_down_period`
  - broken link target: `/compute/docs/autoscaler#stabilization_period`
- **resource-reference/gcp/compute/region-ssl-certificate.mdx**
  - broken link target: `/load-balancing/docs/quotas#ssl_certificates`
- **resource-reference/gcp/compute/ssl-certificate.mdx**
  - broken link target: `/load-balancing/docs/quotas#ssl_certificates`
- **resource-reference/gcp/dataproc/workflow-template.mdx**
  - broken link target: `/dataproc/docs/concepts/workflows/using-workflows#configuring_or_selecting_a_cluster`
- **resource-reference/gcp/secret/manager-secret-version.mdx**
  - broken link target: `/docs/providers/google/guides/using_write_only_arguments.html#updating-write-only-arguments`
- **resource-reference/gcp/sql/database-instance.mdx**
  - broken link target: `/docs/providers/google/guides/using_write_only_arguments.html#updating-write-only-arguments`
- **resource-reference/gcp/vertex/ai-persistent-resource.mdx**
  - broken link target: `/compute/docs/networks-and-firewalls#networks`
  - broken link target: `/compute/docs/reference/rest/v1/networks/insert`

---

## Full list: 449 orphaned index.mdx pages

- resource-reference/aws/accessanalyzer/index.mdx
- resource-reference/aws/account/index.mdx
- resource-reference/aws/acm/index.mdx
- resource-reference/aws/acmpca/index.mdx
- resource-reference/aws/alb/index.mdx
- resource-reference/aws/ami/index.mdx
- resource-reference/aws/amplify/index.mdx
- resource-reference/aws/api/index.mdx
- resource-reference/aws/apigatewayv2/index.mdx
- resource-reference/aws/appautoscaling/index.mdx
- resource-reference/aws/appconfig/index.mdx
- resource-reference/aws/appfabric/index.mdx
- resource-reference/aws/appflow/index.mdx
- resource-reference/aws/appintegrations/index.mdx
- resource-reference/aws/appmesh/index.mdx
- resource-reference/aws/apprunner/index.mdx
- resource-reference/aws/appstream/index.mdx
- resource-reference/aws/appsync/index.mdx
- resource-reference/aws/arczonalshift/index.mdx
- resource-reference/aws/athena/index.mdx
- resource-reference/aws/auditmanager/index.mdx
- resource-reference/aws/autoscaling/index.mdx
- resource-reference/aws/backup/index.mdx
- resource-reference/aws/batch/index.mdx
- resource-reference/aws/bedrock/index.mdx
- resource-reference/aws/bedrockagent/index.mdx
- resource-reference/aws/bedrockagentcore/index.mdx
- resource-reference/aws/budgets/index.mdx
- resource-reference/aws/ce/index.mdx
- resource-reference/aws/chatbot/index.mdx
- resource-reference/aws/chime/index.mdx
- resource-reference/aws/chimesdkvoice/index.mdx
- resource-reference/aws/cleanrooms/index.mdx
- resource-reference/aws/cloud9/index.mdx
- resource-reference/aws/cloudformation/index.mdx
- resource-reference/aws/cloudfront/index.mdx
- resource-reference/aws/cloudfrontkeyvaluestore/index.mdx
- resource-reference/aws/cloudhsm/index.mdx
- resource-reference/aws/cloudsearch/index.mdx
- resource-reference/aws/cloudtrail/index.mdx
- resource-reference/aws/cloudwatch/index.mdx
- resource-reference/aws/codeartifact/index.mdx
- resource-reference/aws/codebuild/index.mdx
- resource-reference/aws/codecatalyst/index.mdx
- resource-reference/aws/codecommit/index.mdx
- resource-reference/aws/codeconnections/index.mdx
- resource-reference/aws/codedeploy/index.mdx
- resource-reference/aws/codepipeline/index.mdx
- resource-reference/aws/codestarconnections/index.mdx
- resource-reference/aws/cognito/index.mdx
- resource-reference/aws/comprehend/index.mdx
- resource-reference/aws/computeoptimizer/index.mdx
- resource-reference/aws/config/index.mdx
- resource-reference/aws/connect/index.mdx
- resource-reference/aws/controltower/index.mdx
- resource-reference/aws/costoptimizationhub/index.mdx
- resource-reference/aws/customerprofiles/index.mdx
- resource-reference/aws/dataexchange/index.mdx
- resource-reference/aws/datapipeline/index.mdx
- resource-reference/aws/datasync/index.mdx
- resource-reference/aws/datazone/index.mdx
- resource-reference/aws/dax/index.mdx
- resource-reference/aws/db/index.mdx
- resource-reference/aws/default/index.mdx
- resource-reference/aws/detective/index.mdx
- resource-reference/aws/devicefarm/index.mdx
- resource-reference/aws/devopsguru/index.mdx
- resource-reference/aws/directory/index.mdx
- resource-reference/aws/dms/index.mdx
- resource-reference/aws/docdb/index.mdx
- resource-reference/aws/dsql/index.mdx
- resource-reference/aws/dx/index.mdx
- resource-reference/aws/dynamodb/index.mdx
- resource-reference/aws/ebs/index.mdx
- resource-reference/aws/ec2/index.mdx
- resource-reference/aws/ecr/index.mdx
- resource-reference/aws/ecrpublic/index.mdx
- resource-reference/aws/ecs/index.mdx
- resource-reference/aws/efs/index.mdx
- resource-reference/aws/eip/index.mdx
- resource-reference/aws/eks/index.mdx
- resource-reference/aws/elastic/index.mdx
- resource-reference/aws/elasticache/index.mdx
- resource-reference/aws/elasticsearch/index.mdx
- resource-reference/aws/elastictranscoder/index.mdx
- resource-reference/aws/elb/index.mdx
- resource-reference/aws/emr/index.mdx
- resource-reference/aws/emrcontainers/index.mdx
- resource-reference/aws/evidently/index.mdx
- resource-reference/aws/finspace/index.mdx
- resource-reference/aws/fis/index.mdx
- resource-reference/aws/fms/index.mdx
- resource-reference/aws/fsx/index.mdx
- resource-reference/aws/gamelift/index.mdx
- resource-reference/aws/glacier/index.mdx
- resource-reference/aws/globalaccelerator/index.mdx
- resource-reference/aws/glue/index.mdx
- resource-reference/aws/grafana/index.mdx
- resource-reference/aws/guardduty/index.mdx
- resource-reference/aws/iam/index.mdx
- resource-reference/aws/identitystore/index.mdx
- resource-reference/aws/imagebuilder/index.mdx
- resource-reference/aws/inspector/index.mdx
- resource-reference/aws/inspector2/index.mdx
- resource-reference/aws/internet/index.mdx
- resource-reference/aws/iot/index.mdx
- resource-reference/aws/ivs/index.mdx
- resource-reference/aws/ivschat/index.mdx
- resource-reference/aws/keyspaces/index.mdx
- resource-reference/aws/kinesis/index.mdx
- resource-reference/aws/kinesisanalyticsv2/index.mdx
- resource-reference/aws/kms/index.mdx
- resource-reference/aws/lakeformation/index.mdx
- resource-reference/aws/lambda/index.mdx
- resource-reference/aws/launch/index.mdx
- resource-reference/aws/lb/index.mdx
- resource-reference/aws/lex/index.mdx
- resource-reference/aws/lexv2models/index.mdx
- resource-reference/aws/licensemanager/index.mdx
- resource-reference/aws/lightsail/index.mdx
- resource-reference/aws/load/index.mdx
- resource-reference/aws/location/index.mdx
- resource-reference/aws/m2/index.mdx
- resource-reference/aws/macie2/index.mdx
- resource-reference/aws/media/index.mdx
- resource-reference/aws/medialive/index.mdx
- resource-reference/aws/memorydb/index.mdx
- resource-reference/aws/mq/index.mdx
- resource-reference/aws/msk/index.mdx
- resource-reference/aws/mskconnect/index.mdx
- resource-reference/aws/nat/index.mdx
- resource-reference/aws/neptune/index.mdx
- resource-reference/aws/network/index.mdx
- resource-reference/aws/networkfirewall/index.mdx
- resource-reference/aws/networkflowmonitor/index.mdx
- resource-reference/aws/networkmanager/index.mdx
- resource-reference/aws/networkmonitor/index.mdx
- resource-reference/aws/notifications/index.mdx
- resource-reference/aws/oam/index.mdx
- resource-reference/aws/observabilityadmin/index.mdx
- resource-reference/aws/odb/index.mdx
- resource-reference/aws/opensearch/index.mdx
- resource-reference/aws/opensearchserverless/index.mdx
- resource-reference/aws/organizations/index.mdx
- resource-reference/aws/osis/index.mdx
- resource-reference/aws/paymentcryptography/index.mdx
- resource-reference/aws/pinpoint/index.mdx
- resource-reference/aws/pinpointsmsvoicev2/index.mdx
- resource-reference/aws/prometheus/index.mdx
- resource-reference/aws/qldb/index.mdx
- resource-reference/aws/quicksight/index.mdx
- resource-reference/aws/ram/index.mdx
- resource-reference/aws/rds/index.mdx
- resource-reference/aws/redshift/index.mdx
- resource-reference/aws/redshiftserverless/index.mdx
- resource-reference/aws/rekognition/index.mdx
- resource-reference/aws/resourcegroups/index.mdx
- resource-reference/aws/rolesanywhere/index.mdx
- resource-reference/aws/route/index.mdx
- resource-reference/aws/route53/index.mdx
- resource-reference/aws/route53domains/index.mdx
- resource-reference/aws/route53profiles/index.mdx
- resource-reference/aws/route53recoverycontrolconfig/index.mdx
- resource-reference/aws/route53recoveryreadiness/index.mdx
- resource-reference/aws/rum/index.mdx
- resource-reference/aws/s3/index.mdx
- resource-reference/aws/s3control/index.mdx
- resource-reference/aws/s3files/index.mdx
- resource-reference/aws/s3tables/index.mdx
- resource-reference/aws/sagemaker/index.mdx
- resource-reference/aws/scheduler/index.mdx
- resource-reference/aws/schemas/index.mdx
- resource-reference/aws/secretsmanager/index.mdx
- resource-reference/aws/security/index.mdx
- resource-reference/aws/securityhub/index.mdx
- resource-reference/aws/securitylake/index.mdx
- resource-reference/aws/service/index.mdx
- resource-reference/aws/servicecatalog/index.mdx
- resource-reference/aws/servicecatalogappregistry/index.mdx
- resource-reference/aws/servicequotas/index.mdx
- resource-reference/aws/ses/index.mdx
- resource-reference/aws/sesv2/index.mdx
- resource-reference/aws/sfn/index.mdx
- resource-reference/aws/shield/index.mdx
- resource-reference/aws/signer/index.mdx
- resource-reference/aws/sns/index.mdx
- resource-reference/aws/spot/index.mdx
- resource-reference/aws/sqs/index.mdx
- resource-reference/aws/ssm/index.mdx
- resource-reference/aws/ssmcontacts/index.mdx
- resource-reference/aws/ssmincidents/index.mdx
- resource-reference/aws/ssoadmin/index.mdx
- resource-reference/aws/storagegateway/index.mdx
- resource-reference/aws/synthetics/index.mdx
- resource-reference/aws/timestreaminfluxdb/index.mdx
- resource-reference/aws/timestreamwrite/index.mdx
- resource-reference/aws/transcribe/index.mdx
- resource-reference/aws/transfer/index.mdx
- resource-reference/aws/verifiedaccess/index.mdx
- resource-reference/aws/verifiedpermissions/index.mdx
- resource-reference/aws/vpc/index.mdx
- resource-reference/aws/vpclattice/index.mdx
- resource-reference/aws/vpn/index.mdx
- resource-reference/aws/waf/index.mdx
- resource-reference/aws/wafregional/index.mdx
- resource-reference/aws/wafv2/index.mdx
- resource-reference/aws/workmail/index.mdx
- resource-reference/aws/workspaces/index.mdx
- resource-reference/aws/workspacesweb/index.mdx
- resource-reference/aws/xray/index.mdx
- resource-reference/azure/active/index.mdx
- resource-reference/azure/ai/index.mdx
- resource-reference/azure/api/index.mdx
- resource-reference/azure/app/index.mdx
- resource-reference/azure/application/index.mdx
- resource-reference/azure/arc/index.mdx
- resource-reference/azure/automation/index.mdx
- resource-reference/azure/backup/index.mdx
- resource-reference/azure/batch/index.mdx
- resource-reference/azure/bot/index.mdx
- resource-reference/azure/capacity/index.mdx
- resource-reference/azure/cdn/index.mdx
- resource-reference/azure/chaos/index.mdx
- resource-reference/azure/cognitive/index.mdx
- resource-reference/azure/communication/index.mdx
- resource-reference/azure/consumption/index.mdx
- resource-reference/azure/container/index.mdx
- resource-reference/azure/cosmosdb/index.mdx
- resource-reference/azure/cost/index.mdx
- resource-reference/azure/custom/index.mdx
- resource-reference/azure/dashboard/index.mdx
- resource-reference/azure/data/index.mdx
- resource-reference/azure/database/index.mdx
- resource-reference/azure/databricks/index.mdx
- resource-reference/azure/datadog/index.mdx
- resource-reference/azure/dedicated/index.mdx
- resource-reference/azure/dev/index.mdx
- resource-reference/azure/digital/index.mdx
- resource-reference/azure/disk/index.mdx
- resource-reference/azure/dns/index.mdx
- resource-reference/azure/dynatrace/index.mdx
- resource-reference/azure/elastic/index.mdx
- resource-reference/azure/email/index.mdx
- resource-reference/azure/eventgrid/index.mdx
- resource-reference/azure/eventhub/index.mdx
- resource-reference/azure/express/index.mdx
- resource-reference/azure/firewall/index.mdx
- resource-reference/azure/frontdoor/index.mdx
- resource-reference/azure/function/index.mdx
- resource-reference/azure/gallery/index.mdx
- resource-reference/azure/hdinsight/index.mdx
- resource-reference/azure/healthcare/index.mdx
- resource-reference/azure/iot/index.mdx
- resource-reference/azure/iotcentral/index.mdx
- resource-reference/azure/iothub/index.mdx
- resource-reference/azure/ip/index.mdx
- resource-reference/azure/key/index.mdx
- resource-reference/azure/kubernetes/index.mdx
- resource-reference/azure/kusto/index.mdx
- resource-reference/azure/lb/index.mdx
- resource-reference/azure/lighthouse/index.mdx
- resource-reference/azure/linux/index.mdx
- resource-reference/azure/log/index.mdx
- resource-reference/azure/logic/index.mdx
- resource-reference/azure/machine/index.mdx
- resource-reference/azure/maintenance/index.mdx
- resource-reference/azure/managed/index.mdx
- resource-reference/azure/management/index.mdx
- resource-reference/azure/marketplace/index.mdx
- resource-reference/azure/mongo/index.mdx
- resource-reference/azure/monitor/index.mdx
- resource-reference/azure/mssql/index.mdx
- resource-reference/azure/mysql/index.mdx
- resource-reference/azure/nat/index.mdx
- resource-reference/azure/netapp/index.mdx
- resource-reference/azure/network/index.mdx
- resource-reference/azure/new/index.mdx
- resource-reference/azure/nginx/index.mdx
- resource-reference/azure/notification/index.mdx
- resource-reference/azure/oracle/index.mdx
- resource-reference/azure/palo/index.mdx
- resource-reference/azure/pim/index.mdx
- resource-reference/azure/policy/index.mdx
- resource-reference/azure/portal/index.mdx
- resource-reference/azure/postgresql/index.mdx
- resource-reference/azure/private/index.mdx
- resource-reference/azure/public/index.mdx
- resource-reference/azure/recovery/index.mdx
- resource-reference/azure/redis/index.mdx
- resource-reference/azure/relay/index.mdx
- resource-reference/azure/resource/index.mdx
- resource-reference/azure/role/index.mdx
- resource-reference/azure/route/index.mdx
- resource-reference/azure/search/index.mdx
- resource-reference/azure/security/index.mdx
- resource-reference/azure/sentinel/index.mdx
- resource-reference/azure/service/index.mdx
- resource-reference/azure/servicebus/index.mdx
- resource-reference/azure/shared/index.mdx
- resource-reference/azure/signalr/index.mdx
- resource-reference/azure/site/index.mdx
- resource-reference/azure/spring/index.mdx
- resource-reference/azure/stack/index.mdx
- resource-reference/azure/static/index.mdx
- resource-reference/azure/storage/index.mdx
- resource-reference/azure/stream/index.mdx
- resource-reference/azure/subnet/index.mdx
- resource-reference/azure/subscription/index.mdx
- resource-reference/azure/synapse/index.mdx
- resource-reference/azure/system/index.mdx
- resource-reference/azure/traffic/index.mdx
- resource-reference/azure/virtual/index.mdx
- resource-reference/azure/vmware/index.mdx
- resource-reference/azure/vpn/index.mdx
- resource-reference/azure/web/index.mdx
- resource-reference/azure/windows/index.mdx
- resource-reference/azure/workloads/index.mdx
- resource-reference/datadog/dashboard/index.mdx
- resource-reference/datadog/synthetics/index.mdx
- resource-reference/datadog/webhooks/index.mdx
- resource-reference/gcp/access/index.mdx
- resource-reference/gcp/active/index.mdx
- resource-reference/gcp/agent/index.mdx
- resource-reference/gcp/alloydb/index.mdx
- resource-reference/gcp/apigee/index.mdx
- resource-reference/gcp/apihub/index.mdx
- resource-reference/gcp/app/index.mdx
- resource-reference/gcp/apphub/index.mdx
- resource-reference/gcp/artifact/index.mdx
- resource-reference/gcp/backup/index.mdx
- resource-reference/gcp/beyondcorp/index.mdx
- resource-reference/gcp/biglake/index.mdx
- resource-reference/gcp/bigquery/analytics-hub-query-template.mdx
- resource-reference/gcp/bigquery/index.mdx
- resource-reference/gcp/bigtable/index.mdx
- resource-reference/gcp/billing/index.mdx
- resource-reference/gcp/binary/index.mdx
- resource-reference/gcp/certificate/index.mdx
- resource-reference/gcp/ces/index.mdx
- resource-reference/gcp/chronicle/index.mdx
- resource-reference/gcp/cloud/index.mdx
- resource-reference/gcp/cloud/support-support-event-subscription.mdx
- resource-reference/gcp/cloudbuild/index.mdx
- resource-reference/gcp/cloudbuildv2/index.mdx
- resource-reference/gcp/clouddeploy/index.mdx
- resource-reference/gcp/cloudfunctions/index.mdx
- resource-reference/gcp/cloudfunctions2/index.mdx
- resource-reference/gcp/colab/index.mdx
- resource-reference/gcp/composer/index.mdx
- resource-reference/gcp/compute/index.mdx
- resource-reference/gcp/contact/index.mdx
- resource-reference/gcp/container/index.mdx
- resource-reference/gcp/data/index.mdx
- resource-reference/gcp/database/index.mdx
- resource-reference/gcp/dataform/index.mdx
- resource-reference/gcp/dataplex/index.mdx
- resource-reference/gcp/dataproc/index.mdx
- resource-reference/gcp/datastream/index.mdx
- resource-reference/gcp/developer/index.mdx
- resource-reference/gcp/dialogflow/index.mdx
- resource-reference/gcp/discovery/index.mdx
- resource-reference/gcp/dns/index.mdx
- resource-reference/gcp/document/index.mdx
- resource-reference/gcp/edgecontainer/index.mdx
- resource-reference/gcp/edgenetwork/index.mdx
- resource-reference/gcp/endpoints/index.mdx
- resource-reference/gcp/eventarc/index.mdx
- resource-reference/gcp/filestore/index.mdx
- resource-reference/gcp/firebase/index.mdx
- resource-reference/gcp/firebaserules/index.mdx
- resource-reference/gcp/folder/index.mdx
- resource-reference/gcp/gemini/index.mdx
- resource-reference/gcp/gke/index.mdx
- resource-reference/gcp/gkeonprem/index.mdx
- resource-reference/gcp/healthcare/index.mdx
- resource-reference/gcp/iam/index.mdx
- resource-reference/gcp/iap/index.mdx
- resource-reference/gcp/identity/index.mdx
- resource-reference/gcp/integration/index.mdx
- resource-reference/gcp/integrations/index.mdx
- resource-reference/gcp/kms/index.mdx
- resource-reference/gcp/logging/index.mdx
- resource-reference/gcp/managed/index.mdx
- resource-reference/gcp/memorystore/index.mdx
- resource-reference/gcp/migration/index.mdx
- resource-reference/gcp/model/index.mdx
- resource-reference/gcp/monitoring/index.mdx
- resource-reference/gcp/netapp/index.mdx
- resource-reference/gcp/network/connectivity-gateway-advertised-route.mdx
- resource-reference/gcp/network/index.mdx
- resource-reference/gcp/notebooks/index.mdx
- resource-reference/gcp/oracle/index.mdx
- resource-reference/gcp/org/index.mdx
- resource-reference/gcp/organization/index.mdx
- resource-reference/gcp/os/index.mdx
- resource-reference/gcp/parameter/index.mdx
- resource-reference/gcp/privateca/index.mdx
- resource-reference/gcp/project/index.mdx
- resource-reference/gcp/pubsub/index.mdx
- resource-reference/gcp/redis/index.mdx
- resource-reference/gcp/resource/index.mdx
- resource-reference/gcp/scc/index.mdx
- resource-reference/gcp/secret/index.mdx
- resource-reference/gcp/secure/index.mdx
- resource-reference/gcp/securityposture/index.mdx
- resource-reference/gcp/service/index.mdx
- resource-reference/gcp/site/index.mdx
- resource-reference/gcp/sourcerepo/index.mdx
- resource-reference/gcp/spanner/index.mdx
- resource-reference/gcp/sql/index.mdx
- resource-reference/gcp/storage/index.mdx
- resource-reference/gcp/tags/index.mdx
- resource-reference/gcp/transcoder/index.mdx
- resource-reference/gcp/vector/index.mdx
- resource-reference/gcp/vertex/index.mdx
- resource-reference/gcp/vmwareengine/index.mdx
- resource-reference/gcp/workbench/index.mdx
- resource-reference/gcp/workstations/index.mdx
- resource-reference/github/actions/index.mdx
- resource-reference/github/check/index.mdx
- resource-reference/github/code/index.mdx
- resource-reference/github/codespace/index.mdx
- resource-reference/github/commit/index.mdx
- resource-reference/github/copilot/index.mdx
- resource-reference/github/deployment/index.mdx
- resource-reference/github/gist/index.mdx
- resource-reference/github/git/index.mdx
- resource-reference/github/issue/index.mdx
- resource-reference/github/org/index.mdx
- resource-reference/github/organization/index.mdx
- resource-reference/github/projects/index.mdx
- resource-reference/github/pull/index.mdx
- resource-reference/github/release/index.mdx
- resource-reference/github/repository/index.mdx
- resource-reference/github/team/index.mdx
- resource-reference/index.mdx
- resource-reference/kubernetes/admissionregistration/index.mdx
- resource-reference/kubernetes/apps/index.mdx
- resource-reference/kubernetes/batch/index.mdx
- resource-reference/kubernetes/certificates/index.mdx
- resource-reference/kubernetes/coordination/index.mdx
- resource-reference/kubernetes/core/index.mdx
- resource-reference/kubernetes/flowcontrol/index.mdx
- resource-reference/kubernetes/lifecycle/index.mdx
- resource-reference/kubernetes/networking/index.mdx
- resource-reference/kubernetes/rbac/index.mdx
- resource-reference/kubernetes/resource/index.mdx
- resource-reference/kubernetes/scheduling/index.mdx
- resource-reference/kubernetes/storage/index.mdx
