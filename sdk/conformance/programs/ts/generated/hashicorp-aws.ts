// Code generated (via sdk/codegen/ir + sdk/codegen/templates/ts, against
// the real hashicorp/aws@6.54.0 provider schema -- the same machinery
// `ubx sdk gen` uses). DO NOT EDIT.
//
// Filtered to aws_db_instance only, unlike a real `ubx sdk gen` run
// (which emits every resource type a provider owns -- 1,682 for this
// exact provider@version, confirmed live, UBI-33/34 session 2) -- this
// conformance fixture only needs the one type its own program uses, and
// committing 1,682 types' worth of bindings for a single-resource golden
// case would dwarf the fixture it supports for no real benefit.
export const __ubxSourceProvenance = { source: "hashicorp/aws", version: "6.54.0" } as const;

import type { Computed, FieldMap, ResourceBinding } from "@ubx/sdk";

export interface AwsDbInstance_ListenerEndpoint {
  address: string;
  hostedZoneId: string;
  port: number;
}

export interface AwsDbInstance_MasterUserSecret {
  kmsKeyId: string;
  secretArn: string;
  secretStatus: string;
}

export interface AwsDbInstance_BlueGreenUpdate {
  enabled: boolean;
}

export interface AwsDbInstance_RestoreToPointInTime {
  restoreTime: string;
  sourceDbInstanceAutomatedBackupsArn: string;
  sourceDbInstanceIdentifier: string;
  sourceDbiResourceId: string;
  useLatestRestorableTime: boolean;
}

export interface AwsDbInstance_S3Import {
  bucketName: string;
  bucketPrefix: string;
  ingestionRole: string;
  sourceEngine: string;
  sourceEngineVersion: string;
}

export interface AwsDbInstance_Timeouts {
  create: string;
  delete: string;
  update: string;
}

export interface AwsDbInstanceConfig {
  allocatedStorage?: number | Computed<number>;
  allowMajorVersionUpgrade?: boolean | Computed<boolean>;
  applyImmediately?: boolean | Computed<boolean>;
  autoMinorVersionUpgrade?: boolean | Computed<boolean>;
  availabilityZone?: string | Computed<string>;
  backupRetentionPeriod?: number | Computed<number>;
  backupTarget?: string | Computed<string>;
  backupWindow?: string | Computed<string>;
  caCertIdentifier?: string | Computed<string>;
  characterSetName?: string | Computed<string>;
  copyTagsToSnapshot?: boolean | Computed<boolean>;
  customIamInstanceProfile?: string | Computed<string>;
  customerOwnedIpEnabled?: boolean | Computed<boolean>;
  databaseInsightsMode?: string | Computed<string>;
  dbName?: string | Computed<string>;
  dbSubnetGroupName?: string | Computed<string>;
  dedicatedLogVolume?: boolean | Computed<boolean>;
  deleteAutomatedBackups?: boolean | Computed<boolean>;
  deletionProtection?: boolean | Computed<boolean>;
  domain?: string | Computed<string>;
  domainAuthSecretArn?: string | Computed<string>;
  domainDnsIps?: string[] | Computed<string[]>;
  domainFqdn?: string | Computed<string>;
  domainIamRoleName?: string | Computed<string>;
  domainOu?: string | Computed<string>;
  enabledCloudwatchLogsExports?: string[] | Computed<string[]>;
  engine?: string | Computed<string>;
  engineLifecycleSupport?: string | Computed<string>;
  engineVersion?: string | Computed<string>;
  finalSnapshotIdentifier?: string | Computed<string>;
  iamDatabaseAuthenticationEnabled?: boolean | Computed<boolean>;
  id?: string | Computed<string>;
  identifier?: string | Computed<string>;
  identifierPrefix?: string | Computed<string>;
  instanceClass: string | Computed<string>;
  iops?: number | Computed<number>;
  kmsKeyId?: string | Computed<string>;
  licenseModel?: string | Computed<string>;
  maintenanceWindow?: string | Computed<string>;
  manageMasterUserPassword?: boolean | Computed<boolean>;
  masterUserSecretKmsKeyId?: string | Computed<string>;
  maxAllocatedStorage?: number | Computed<number>;
  monitoringInterval?: number | Computed<number>;
  monitoringRoleArn?: string | Computed<string>;
  multiAz?: boolean | Computed<boolean>;
  ncharCharacterSetName?: string | Computed<string>;
  networkType?: string | Computed<string>;
  optionGroupName?: string | Computed<string>;
  parameterGroupName?: string | Computed<string>;
  password?: string | Computed<string>;
  passwordWo?: string | Computed<string>;
  passwordWoVersion?: number | Computed<number>;
  performanceInsightsEnabled?: boolean | Computed<boolean>;
  performanceInsightsKmsKeyId?: string | Computed<string>;
  performanceInsightsRetentionPeriod?: number | Computed<number>;
  port?: number | Computed<number>;
  publiclyAccessible?: boolean | Computed<boolean>;
  region?: string | Computed<string>;
  replicaMode?: string | Computed<string>;
  replicateSourceDb?: string | Computed<string>;
  skipFinalSnapshot?: boolean | Computed<boolean>;
  snapshotIdentifier?: string | Computed<string>;
  storageEncrypted?: boolean | Computed<boolean>;
  storageThroughput?: number | Computed<number>;
  storageType?: string | Computed<string>;
  tags?: Record<string, string> | Computed<Record<string, string>>;
  tagsAll?: Record<string, string> | Computed<Record<string, string>>;
  timezone?: string | Computed<string>;
  upgradeStorageConfig?: boolean | Computed<boolean>;
  username?: string | Computed<string>;
  vpcSecurityGroupIds?: string[] | Computed<string[]>;
  blueGreenUpdate?: AwsDbInstance_BlueGreenUpdate[] | Computed<AwsDbInstance_BlueGreenUpdate[]>;
  restoreToPointInTime?: AwsDbInstance_RestoreToPointInTime[] | Computed<AwsDbInstance_RestoreToPointInTime[]>;
  s3Import?: AwsDbInstance_S3Import[] | Computed<AwsDbInstance_S3Import[]>;
  timeouts?: AwsDbInstance_Timeouts | Computed<AwsDbInstance_Timeouts>;
}

export interface AwsDbInstanceAttrs {
  address: string;
  allocatedStorage: number;
  allowMajorVersionUpgrade: boolean;
  applyImmediately: boolean;
  arn: string;
  autoMinorVersionUpgrade: boolean;
  availabilityZone: string;
  backupRetentionPeriod: number;
  backupTarget: string;
  backupWindow: string;
  caCertIdentifier: string;
  characterSetName: string;
  copyTagsToSnapshot: boolean;
  customIamInstanceProfile: string;
  customerOwnedIpEnabled: boolean;
  databaseInsightsMode: string;
  dbName: string;
  dbSubnetGroupName: string;
  dedicatedLogVolume: boolean;
  deleteAutomatedBackups: boolean;
  deletionProtection: boolean;
  domain: string;
  domainAuthSecretArn: string;
  domainDnsIps: string[];
  domainFqdn: string;
  domainIamRoleName: string;
  domainOu: string;
  enabledCloudwatchLogsExports: string[];
  endpoint: string;
  engine: string;
  engineLifecycleSupport: string;
  engineVersion: string;
  engineVersionActual: string;
  finalSnapshotIdentifier: string;
  hostedZoneId: string;
  iamDatabaseAuthenticationEnabled: boolean;
  id: string;
  identifier: string;
  identifierPrefix: string;
  instanceClass: string;
  iops: number;
  kmsKeyId: string;
  latestRestorableTime: string;
  licenseModel: string;
  listenerEndpoint: AwsDbInstance_ListenerEndpoint[];
  maintenanceWindow: string;
  manageMasterUserPassword: boolean;
  masterUserSecret: AwsDbInstance_MasterUserSecret[];
  masterUserSecretKmsKeyId: string;
  maxAllocatedStorage: number;
  monitoringInterval: number;
  monitoringRoleArn: string;
  multiAz: boolean;
  ncharCharacterSetName: string;
  networkType: string;
  optionGroupName: string;
  parameterGroupName: string;
  password: string;
  passwordWo: string;
  passwordWoVersion: number;
  performanceInsightsEnabled: boolean;
  performanceInsightsKmsKeyId: string;
  performanceInsightsRetentionPeriod: number;
  port: number;
  publiclyAccessible: boolean;
  region: string;
  replicaMode: string;
  replicas: string[];
  replicateSourceDb: string;
  resourceId: string;
  skipFinalSnapshot: boolean;
  snapshotIdentifier: string;
  status: string;
  storageEncrypted: boolean;
  storageThroughput: number;
  storageType: string;
  tags: Record<string, string>;
  tagsAll: Record<string, string>;
  timezone: string;
  upgradeRolloutOrder: string;
  upgradeStorageConfig: boolean;
  username: string;
  vpcSecurityGroupIds: string[];
  blueGreenUpdate: AwsDbInstance_BlueGreenUpdate[];
  restoreToPointInTime: AwsDbInstance_RestoreToPointInTime[];
  s3Import: AwsDbInstance_S3Import[];
  timeouts: AwsDbInstance_Timeouts;
}

export const AwsDbInstance: ResourceBinding<AwsDbInstanceConfig, AwsDbInstanceAttrs> = {
  wireType: "aws_db_instance",
  fields: {
    allocatedStorage: "allocated_storage",
    allowMajorVersionUpgrade: "allow_major_version_upgrade",
    applyImmediately: "apply_immediately",
    autoMinorVersionUpgrade: "auto_minor_version_upgrade",
    availabilityZone: "availability_zone",
    backupRetentionPeriod: "backup_retention_period",
    backupTarget: "backup_target",
    backupWindow: "backup_window",
    caCertIdentifier: "ca_cert_identifier",
    characterSetName: "character_set_name",
    copyTagsToSnapshot: "copy_tags_to_snapshot",
    customIamInstanceProfile: "custom_iam_instance_profile",
    customerOwnedIpEnabled: "customer_owned_ip_enabled",
    databaseInsightsMode: "database_insights_mode",
    dbName: "db_name",
    dbSubnetGroupName: "db_subnet_group_name",
    dedicatedLogVolume: "dedicated_log_volume",
    deleteAutomatedBackups: "delete_automated_backups",
    deletionProtection: "deletion_protection",
    domain: "domain",
    domainAuthSecretArn: "domain_auth_secret_arn",
    domainDnsIps: "domain_dns_ips",
    domainFqdn: "domain_fqdn",
    domainIamRoleName: "domain_iam_role_name",
    domainOu: "domain_ou",
    enabledCloudwatchLogsExports: "enabled_cloudwatch_logs_exports",
    engine: "engine",
    engineLifecycleSupport: "engine_lifecycle_support",
    engineVersion: "engine_version",
    finalSnapshotIdentifier: "final_snapshot_identifier",
    iamDatabaseAuthenticationEnabled: "iam_database_authentication_enabled",
    id: "id",
    identifier: "identifier",
    identifierPrefix: "identifier_prefix",
    instanceClass: "instance_class",
    iops: "iops",
    kmsKeyId: "kms_key_id",
    licenseModel: "license_model",
    maintenanceWindow: "maintenance_window",
    manageMasterUserPassword: "manage_master_user_password",
    masterUserSecretKmsKeyId: "master_user_secret_kms_key_id",
    maxAllocatedStorage: "max_allocated_storage",
    monitoringInterval: "monitoring_interval",
    monitoringRoleArn: "monitoring_role_arn",
    multiAz: "multi_az",
    ncharCharacterSetName: "nchar_character_set_name",
    networkType: "network_type",
    optionGroupName: "option_group_name",
    parameterGroupName: "parameter_group_name",
    password: "password",
    passwordWo: "password_wo",
    passwordWoVersion: "password_wo_version",
    performanceInsightsEnabled: "performance_insights_enabled",
    performanceInsightsKmsKeyId: "performance_insights_kms_key_id",
    performanceInsightsRetentionPeriod: "performance_insights_retention_period",
    port: "port",
    publiclyAccessible: "publicly_accessible",
    region: "region",
    replicaMode: "replica_mode",
    replicateSourceDb: "replicate_source_db",
    skipFinalSnapshot: "skip_final_snapshot",
    snapshotIdentifier: "snapshot_identifier",
    storageEncrypted: "storage_encrypted",
    storageThroughput: "storage_throughput",
    storageType: "storage_type",
    tags: "tags",
    tagsAll: "tags_all",
    timezone: "timezone",
    upgradeStorageConfig: "upgrade_storage_config",
    username: "username",
    vpcSecurityGroupIds: "vpc_security_group_ids",
    blueGreenUpdate: {
      wireName: "blue_green_update",
      kind: "list",
      fields: {
        enabled: "enabled",
      },
    },
    restoreToPointInTime: {
      wireName: "restore_to_point_in_time",
      kind: "list",
      fields: {
        restoreTime: "restore_time",
        sourceDbInstanceAutomatedBackupsArn: "source_db_instance_automated_backups_arn",
        sourceDbInstanceIdentifier: "source_db_instance_identifier",
        sourceDbiResourceId: "source_dbi_resource_id",
        useLatestRestorableTime: "use_latest_restorable_time",
      },
    },
    s3Import: {
      wireName: "s3_import",
      kind: "list",
      fields: {
        bucketName: "bucket_name",
        bucketPrefix: "bucket_prefix",
        ingestionRole: "ingestion_role",
        sourceEngine: "source_engine",
        sourceEngineVersion: "source_engine_version",
      },
    },
    timeouts: {
      wireName: "timeouts",
      kind: "object",
      fields: {
        create: "create",
        delete: "delete",
        update: "update",
      },
    },
  },
};

