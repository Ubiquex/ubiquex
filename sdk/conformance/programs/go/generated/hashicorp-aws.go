// Code generated (via sdk/codegen/ir + sdk/codegen/templates/go, against
// the real hashicorp/aws@6.54.0 provider schema -- the same machinery
// `ubx sdk gen --lang go` uses). DO NOT EDIT.
//
// Filtered to aws_db_instance only, unlike a real `ubx sdk gen` run
// (which emits every resource type a provider owns -- 1,682 for this
// exact provider@version, confirmed live this session, matching the TS
// conformance fixture's own real figure from UBI-33/34 session 2) --
// this conformance fixture only needs the one type its own program
// uses, and committing 1,682 types' worth of bindings for a single-
// resource golden case would dwarf the fixture it supports for no real
// benefit.
package generated

import sdk "github.com/ubiquex/ubx-sdk-go/runtime"

var SourceProvenance = struct {
	Source  string
	Version string
}{Source: "hashicorp/aws", Version: "6.54.0"}

type AwsDbInstance_BlueGreenUpdate struct {
	Enabled any
}

type AwsDbInstance_RestoreToPointInTime struct {
	RestoreTime                         any
	SourceDbInstanceAutomatedBackupsArn any
	SourceDbInstanceIdentifier          any
	SourceDbiResourceId                 any
	UseLatestRestorableTime             any
}

type AwsDbInstance_S3Import struct {
	BucketName          any
	BucketPrefix        any
	IngestionRole       any
	SourceEngine        any
	SourceEngineVersion any
}

type AwsDbInstance_Timeouts struct {
	Create any
	Delete any
	Update any
}

type AwsDbInstanceConfig struct {
	AllocatedStorage                   any
	AllowMajorVersionUpgrade           any
	ApplyImmediately                   any
	AutoMinorVersionUpgrade            any
	AvailabilityZone                   any
	BackupRetentionPeriod              any
	BackupTarget                       any
	BackupWindow                       any
	CaCertIdentifier                   any
	CharacterSetName                   any
	CopyTagsToSnapshot                 any
	CustomIamInstanceProfile           any
	CustomerOwnedIpEnabled             any
	DatabaseInsightsMode               any
	DbName                             any
	DbSubnetGroupName                  any
	DedicatedLogVolume                 any
	DeleteAutomatedBackups             any
	DeletionProtection                 any
	Domain                             any
	DomainAuthSecretArn                any
	DomainDnsIps                       any
	DomainFqdn                         any
	DomainIamRoleName                  any
	DomainOu                           any
	EnabledCloudwatchLogsExports       any
	Engine                             any
	EngineLifecycleSupport             any
	EngineVersion                      any
	FinalSnapshotIdentifier            any
	IamDatabaseAuthenticationEnabled   any
	Id                                 any
	Identifier                         any
	IdentifierPrefix                   any
	InstanceClass                      any
	Iops                               any
	KmsKeyId                           any
	LicenseModel                       any
	MaintenanceWindow                  any
	ManageMasterUserPassword           any
	MasterUserSecretKmsKeyId           any
	MaxAllocatedStorage                any
	MonitoringInterval                 any
	MonitoringRoleArn                  any
	MultiAz                            any
	NcharCharacterSetName              any
	NetworkType                        any
	OptionGroupName                    any
	ParameterGroupName                 any
	Password                           any
	PasswordWo                         any
	PasswordWoVersion                  any
	PerformanceInsightsEnabled         any
	PerformanceInsightsKmsKeyId        any
	PerformanceInsightsRetentionPeriod any
	Port                               any
	PubliclyAccessible                 any
	Region                             any
	ReplicaMode                        any
	ReplicateSourceDb                  any
	SkipFinalSnapshot                  any
	SnapshotIdentifier                 any
	StorageEncrypted                   any
	StorageThroughput                  any
	StorageType                        any
	Tags                               any
	TagsAll                            any
	Timezone                           any
	UpgradeStorageConfig               any
	Username                           any
	VpcSecurityGroupIds                any
	BlueGreenUpdate                    any
	RestoreToPointInTime               any
	S3Import                           any
	Timeouts                           any
}

var AwsDbInstance = sdk.ResourceBinding{
	WireType: "aws_db_instance",
	Fields: sdk.FieldMap{
		"AllocatedStorage":                   sdk.FieldSpec{WireName: "allocated_storage"},
		"AllowMajorVersionUpgrade":           sdk.FieldSpec{WireName: "allow_major_version_upgrade"},
		"ApplyImmediately":                   sdk.FieldSpec{WireName: "apply_immediately"},
		"AutoMinorVersionUpgrade":            sdk.FieldSpec{WireName: "auto_minor_version_upgrade"},
		"AvailabilityZone":                   sdk.FieldSpec{WireName: "availability_zone"},
		"BackupRetentionPeriod":              sdk.FieldSpec{WireName: "backup_retention_period"},
		"BackupTarget":                       sdk.FieldSpec{WireName: "backup_target"},
		"BackupWindow":                       sdk.FieldSpec{WireName: "backup_window"},
		"CaCertIdentifier":                   sdk.FieldSpec{WireName: "ca_cert_identifier"},
		"CharacterSetName":                   sdk.FieldSpec{WireName: "character_set_name"},
		"CopyTagsToSnapshot":                 sdk.FieldSpec{WireName: "copy_tags_to_snapshot"},
		"CustomIamInstanceProfile":           sdk.FieldSpec{WireName: "custom_iam_instance_profile"},
		"CustomerOwnedIpEnabled":             sdk.FieldSpec{WireName: "customer_owned_ip_enabled"},
		"DatabaseInsightsMode":               sdk.FieldSpec{WireName: "database_insights_mode"},
		"DbName":                             sdk.FieldSpec{WireName: "db_name"},
		"DbSubnetGroupName":                  sdk.FieldSpec{WireName: "db_subnet_group_name"},
		"DedicatedLogVolume":                 sdk.FieldSpec{WireName: "dedicated_log_volume"},
		"DeleteAutomatedBackups":             sdk.FieldSpec{WireName: "delete_automated_backups"},
		"DeletionProtection":                 sdk.FieldSpec{WireName: "deletion_protection"},
		"Domain":                             sdk.FieldSpec{WireName: "domain"},
		"DomainAuthSecretArn":                sdk.FieldSpec{WireName: "domain_auth_secret_arn"},
		"DomainDnsIps":                       sdk.FieldSpec{WireName: "domain_dns_ips"},
		"DomainFqdn":                         sdk.FieldSpec{WireName: "domain_fqdn"},
		"DomainIamRoleName":                  sdk.FieldSpec{WireName: "domain_iam_role_name"},
		"DomainOu":                           sdk.FieldSpec{WireName: "domain_ou"},
		"EnabledCloudwatchLogsExports":       sdk.FieldSpec{WireName: "enabled_cloudwatch_logs_exports"},
		"Engine":                             sdk.FieldSpec{WireName: "engine"},
		"EngineLifecycleSupport":             sdk.FieldSpec{WireName: "engine_lifecycle_support"},
		"EngineVersion":                      sdk.FieldSpec{WireName: "engine_version"},
		"FinalSnapshotIdentifier":            sdk.FieldSpec{WireName: "final_snapshot_identifier"},
		"IamDatabaseAuthenticationEnabled":   sdk.FieldSpec{WireName: "iam_database_authentication_enabled"},
		"Id":                                 sdk.FieldSpec{WireName: "id"},
		"Identifier":                         sdk.FieldSpec{WireName: "identifier"},
		"IdentifierPrefix":                   sdk.FieldSpec{WireName: "identifier_prefix"},
		"InstanceClass":                      sdk.FieldSpec{WireName: "instance_class"},
		"Iops":                               sdk.FieldSpec{WireName: "iops"},
		"KmsKeyId":                           sdk.FieldSpec{WireName: "kms_key_id"},
		"LicenseModel":                       sdk.FieldSpec{WireName: "license_model"},
		"MaintenanceWindow":                  sdk.FieldSpec{WireName: "maintenance_window"},
		"ManageMasterUserPassword":           sdk.FieldSpec{WireName: "manage_master_user_password"},
		"MasterUserSecretKmsKeyId":           sdk.FieldSpec{WireName: "master_user_secret_kms_key_id"},
		"MaxAllocatedStorage":                sdk.FieldSpec{WireName: "max_allocated_storage"},
		"MonitoringInterval":                 sdk.FieldSpec{WireName: "monitoring_interval"},
		"MonitoringRoleArn":                  sdk.FieldSpec{WireName: "monitoring_role_arn"},
		"MultiAz":                            sdk.FieldSpec{WireName: "multi_az"},
		"NcharCharacterSetName":              sdk.FieldSpec{WireName: "nchar_character_set_name"},
		"NetworkType":                        sdk.FieldSpec{WireName: "network_type"},
		"OptionGroupName":                    sdk.FieldSpec{WireName: "option_group_name"},
		"ParameterGroupName":                 sdk.FieldSpec{WireName: "parameter_group_name"},
		"Password":                           sdk.FieldSpec{WireName: "password"},
		"PasswordWo":                         sdk.FieldSpec{WireName: "password_wo"},
		"PasswordWoVersion":                  sdk.FieldSpec{WireName: "password_wo_version"},
		"PerformanceInsightsEnabled":         sdk.FieldSpec{WireName: "performance_insights_enabled"},
		"PerformanceInsightsKmsKeyId":        sdk.FieldSpec{WireName: "performance_insights_kms_key_id"},
		"PerformanceInsightsRetentionPeriod": sdk.FieldSpec{WireName: "performance_insights_retention_period"},
		"Port":                               sdk.FieldSpec{WireName: "port"},
		"PubliclyAccessible":                 sdk.FieldSpec{WireName: "publicly_accessible"},
		"Region":                             sdk.FieldSpec{WireName: "region"},
		"ReplicaMode":                        sdk.FieldSpec{WireName: "replica_mode"},
		"ReplicateSourceDb":                  sdk.FieldSpec{WireName: "replicate_source_db"},
		"SkipFinalSnapshot":                  sdk.FieldSpec{WireName: "skip_final_snapshot"},
		"SnapshotIdentifier":                 sdk.FieldSpec{WireName: "snapshot_identifier"},
		"StorageEncrypted":                   sdk.FieldSpec{WireName: "storage_encrypted"},
		"StorageThroughput":                  sdk.FieldSpec{WireName: "storage_throughput"},
		"StorageType":                        sdk.FieldSpec{WireName: "storage_type"},
		"Tags":                               sdk.FieldSpec{WireName: "tags"},
		"TagsAll":                            sdk.FieldSpec{WireName: "tags_all"},
		"Timezone":                           sdk.FieldSpec{WireName: "timezone"},
		"UpgradeStorageConfig":               sdk.FieldSpec{WireName: "upgrade_storage_config"},
		"Username":                           sdk.FieldSpec{WireName: "username"},
		"VpcSecurityGroupIds":                sdk.FieldSpec{WireName: "vpc_security_group_ids"},
		"BlueGreenUpdate": sdk.FieldSpec{
			WireName: "blue_green_update",
			Kind:     "list",
			Fields: sdk.FieldMap{
				"Enabled": sdk.FieldSpec{WireName: "enabled"},
			},
		},
		"RestoreToPointInTime": sdk.FieldSpec{
			WireName: "restore_to_point_in_time",
			Kind:     "list",
			Fields: sdk.FieldMap{
				"RestoreTime":                         sdk.FieldSpec{WireName: "restore_time"},
				"SourceDbInstanceAutomatedBackupsArn": sdk.FieldSpec{WireName: "source_db_instance_automated_backups_arn"},
				"SourceDbInstanceIdentifier":          sdk.FieldSpec{WireName: "source_db_instance_identifier"},
				"SourceDbiResourceId":                 sdk.FieldSpec{WireName: "source_dbi_resource_id"},
				"UseLatestRestorableTime":             sdk.FieldSpec{WireName: "use_latest_restorable_time"},
			},
		},
		"S3Import": sdk.FieldSpec{
			WireName: "s3_import",
			Kind:     "list",
			Fields: sdk.FieldMap{
				"BucketName":          sdk.FieldSpec{WireName: "bucket_name"},
				"BucketPrefix":        sdk.FieldSpec{WireName: "bucket_prefix"},
				"IngestionRole":       sdk.FieldSpec{WireName: "ingestion_role"},
				"SourceEngine":        sdk.FieldSpec{WireName: "source_engine"},
				"SourceEngineVersion": sdk.FieldSpec{WireName: "source_engine_version"},
			},
		},
		"Timeouts": sdk.FieldSpec{
			WireName: "timeouts",
			Kind:     "object",
			Fields: sdk.FieldMap{
				"Create": sdk.FieldSpec{WireName: "create"},
				"Delete": sdk.FieldSpec{WireName: "delete"},
				"Update": sdk.FieldSpec{WireName: "update"},
			},
		},
	},
}
