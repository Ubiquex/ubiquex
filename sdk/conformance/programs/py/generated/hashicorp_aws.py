# Code generated (via sdk/codegen/ir + sdk/codegen/templates/py, against
# the real hashicorp/aws@6.54.0 provider schema -- the same machinery
# `ubx sdk gen --lang py` uses). DO NOT EDIT.
#
# Filtered to aws_db_instance only, unlike a real `ubx sdk gen` run
# (which emits every resource type a provider owns -- 1,682 for this
# exact provider@version, confirmed live this session, matching the
# TS/Go conformance fixtures' own real figure). This conformance fixture
# only needs the one type its own program uses.
from __future__ import annotations

import dataclasses
from typing import Any

import ubx_sdk as sdk

SOURCE_PROVENANCE = {"source": "hashicorp/aws", "version": "6.54.0"}

@dataclasses.dataclass
class AwsDbInstance_BlueGreenUpdate:
    enabled: Any = None

@dataclasses.dataclass
class AwsDbInstance_RestoreToPointInTime:
    restore_time: Any = None
    source_db_instance_automated_backups_arn: Any = None
    source_db_instance_identifier: Any = None
    source_dbi_resource_id: Any = None
    use_latest_restorable_time: Any = None

@dataclasses.dataclass
class AwsDbInstance_S3Import:
    bucket_name: Any = None
    bucket_prefix: Any = None
    ingestion_role: Any = None
    source_engine: Any = None
    source_engine_version: Any = None

@dataclasses.dataclass
class AwsDbInstance_Timeouts:
    create: Any = None
    delete: Any = None
    update: Any = None

@dataclasses.dataclass
class AwsDbInstanceConfig:
    allocated_storage: Any = None
    allow_major_version_upgrade: Any = None
    apply_immediately: Any = None
    auto_minor_version_upgrade: Any = None
    availability_zone: Any = None
    backup_retention_period: Any = None
    backup_target: Any = None
    backup_window: Any = None
    ca_cert_identifier: Any = None
    character_set_name: Any = None
    copy_tags_to_snapshot: Any = None
    custom_iam_instance_profile: Any = None
    customer_owned_ip_enabled: Any = None
    database_insights_mode: Any = None
    db_name: Any = None
    db_subnet_group_name: Any = None
    dedicated_log_volume: Any = None
    delete_automated_backups: Any = None
    deletion_protection: Any = None
    domain: Any = None
    domain_auth_secret_arn: Any = None
    domain_dns_ips: Any = None
    domain_fqdn: Any = None
    domain_iam_role_name: Any = None
    domain_ou: Any = None
    enabled_cloudwatch_logs_exports: Any = None
    engine: Any = None
    engine_lifecycle_support: Any = None
    engine_version: Any = None
    final_snapshot_identifier: Any = None
    iam_database_authentication_enabled: Any = None
    id: Any = None
    identifier: Any = None
    identifier_prefix: Any = None
    instance_class: Any = None
    iops: Any = None
    kms_key_id: Any = None
    license_model: Any = None
    maintenance_window: Any = None
    manage_master_user_password: Any = None
    master_user_secret_kms_key_id: Any = None
    max_allocated_storage: Any = None
    monitoring_interval: Any = None
    monitoring_role_arn: Any = None
    multi_az: Any = None
    nchar_character_set_name: Any = None
    network_type: Any = None
    option_group_name: Any = None
    parameter_group_name: Any = None
    password: Any = None
    password_wo: Any = None
    password_wo_version: Any = None
    performance_insights_enabled: Any = None
    performance_insights_kms_key_id: Any = None
    performance_insights_retention_period: Any = None
    port: Any = None
    publicly_accessible: Any = None
    region: Any = None
    replica_mode: Any = None
    replicate_source_db: Any = None
    skip_final_snapshot: Any = None
    snapshot_identifier: Any = None
    storage_encrypted: Any = None
    storage_throughput: Any = None
    storage_type: Any = None
    tags: Any = None
    tags_all: Any = None
    timezone: Any = None
    upgrade_storage_config: Any = None
    username: Any = None
    vpc_security_group_ids: Any = None
    blue_green_update: Any = None
    restore_to_point_in_time: Any = None
    s3_import: Any = None
    timeouts: Any = None

AwsDbInstance = sdk.ResourceBinding(
    wire_type="aws_db_instance",
    fields={
        "allocated_storage": sdk.FieldSpec(wire_name="allocated_storage"),
        "allow_major_version_upgrade": sdk.FieldSpec(wire_name="allow_major_version_upgrade"),
        "apply_immediately": sdk.FieldSpec(wire_name="apply_immediately"),
        "auto_minor_version_upgrade": sdk.FieldSpec(wire_name="auto_minor_version_upgrade"),
        "availability_zone": sdk.FieldSpec(wire_name="availability_zone"),
        "backup_retention_period": sdk.FieldSpec(wire_name="backup_retention_period"),
        "backup_target": sdk.FieldSpec(wire_name="backup_target"),
        "backup_window": sdk.FieldSpec(wire_name="backup_window"),
        "ca_cert_identifier": sdk.FieldSpec(wire_name="ca_cert_identifier"),
        "character_set_name": sdk.FieldSpec(wire_name="character_set_name"),
        "copy_tags_to_snapshot": sdk.FieldSpec(wire_name="copy_tags_to_snapshot"),
        "custom_iam_instance_profile": sdk.FieldSpec(wire_name="custom_iam_instance_profile"),
        "customer_owned_ip_enabled": sdk.FieldSpec(wire_name="customer_owned_ip_enabled"),
        "database_insights_mode": sdk.FieldSpec(wire_name="database_insights_mode"),
        "db_name": sdk.FieldSpec(wire_name="db_name"),
        "db_subnet_group_name": sdk.FieldSpec(wire_name="db_subnet_group_name"),
        "dedicated_log_volume": sdk.FieldSpec(wire_name="dedicated_log_volume"),
        "delete_automated_backups": sdk.FieldSpec(wire_name="delete_automated_backups"),
        "deletion_protection": sdk.FieldSpec(wire_name="deletion_protection"),
        "domain": sdk.FieldSpec(wire_name="domain"),
        "domain_auth_secret_arn": sdk.FieldSpec(wire_name="domain_auth_secret_arn"),
        "domain_dns_ips": sdk.FieldSpec(wire_name="domain_dns_ips"),
        "domain_fqdn": sdk.FieldSpec(wire_name="domain_fqdn"),
        "domain_iam_role_name": sdk.FieldSpec(wire_name="domain_iam_role_name"),
        "domain_ou": sdk.FieldSpec(wire_name="domain_ou"),
        "enabled_cloudwatch_logs_exports": sdk.FieldSpec(wire_name="enabled_cloudwatch_logs_exports"),
        "engine": sdk.FieldSpec(wire_name="engine"),
        "engine_lifecycle_support": sdk.FieldSpec(wire_name="engine_lifecycle_support"),
        "engine_version": sdk.FieldSpec(wire_name="engine_version"),
        "final_snapshot_identifier": sdk.FieldSpec(wire_name="final_snapshot_identifier"),
        "iam_database_authentication_enabled": sdk.FieldSpec(wire_name="iam_database_authentication_enabled"),
        "id": sdk.FieldSpec(wire_name="id"),
        "identifier": sdk.FieldSpec(wire_name="identifier"),
        "identifier_prefix": sdk.FieldSpec(wire_name="identifier_prefix"),
        "instance_class": sdk.FieldSpec(wire_name="instance_class"),
        "iops": sdk.FieldSpec(wire_name="iops"),
        "kms_key_id": sdk.FieldSpec(wire_name="kms_key_id"),
        "license_model": sdk.FieldSpec(wire_name="license_model"),
        "maintenance_window": sdk.FieldSpec(wire_name="maintenance_window"),
        "manage_master_user_password": sdk.FieldSpec(wire_name="manage_master_user_password"),
        "master_user_secret_kms_key_id": sdk.FieldSpec(wire_name="master_user_secret_kms_key_id"),
        "max_allocated_storage": sdk.FieldSpec(wire_name="max_allocated_storage"),
        "monitoring_interval": sdk.FieldSpec(wire_name="monitoring_interval"),
        "monitoring_role_arn": sdk.FieldSpec(wire_name="monitoring_role_arn"),
        "multi_az": sdk.FieldSpec(wire_name="multi_az"),
        "nchar_character_set_name": sdk.FieldSpec(wire_name="nchar_character_set_name"),
        "network_type": sdk.FieldSpec(wire_name="network_type"),
        "option_group_name": sdk.FieldSpec(wire_name="option_group_name"),
        "parameter_group_name": sdk.FieldSpec(wire_name="parameter_group_name"),
        "password": sdk.FieldSpec(wire_name="password"),
        "password_wo": sdk.FieldSpec(wire_name="password_wo"),
        "password_wo_version": sdk.FieldSpec(wire_name="password_wo_version"),
        "performance_insights_enabled": sdk.FieldSpec(wire_name="performance_insights_enabled"),
        "performance_insights_kms_key_id": sdk.FieldSpec(wire_name="performance_insights_kms_key_id"),
        "performance_insights_retention_period": sdk.FieldSpec(wire_name="performance_insights_retention_period"),
        "port": sdk.FieldSpec(wire_name="port"),
        "publicly_accessible": sdk.FieldSpec(wire_name="publicly_accessible"),
        "region": sdk.FieldSpec(wire_name="region"),
        "replica_mode": sdk.FieldSpec(wire_name="replica_mode"),
        "replicate_source_db": sdk.FieldSpec(wire_name="replicate_source_db"),
        "skip_final_snapshot": sdk.FieldSpec(wire_name="skip_final_snapshot"),
        "snapshot_identifier": sdk.FieldSpec(wire_name="snapshot_identifier"),
        "storage_encrypted": sdk.FieldSpec(wire_name="storage_encrypted"),
        "storage_throughput": sdk.FieldSpec(wire_name="storage_throughput"),
        "storage_type": sdk.FieldSpec(wire_name="storage_type"),
        "tags": sdk.FieldSpec(wire_name="tags"),
        "tags_all": sdk.FieldSpec(wire_name="tags_all"),
        "timezone": sdk.FieldSpec(wire_name="timezone"),
        "upgrade_storage_config": sdk.FieldSpec(wire_name="upgrade_storage_config"),
        "username": sdk.FieldSpec(wire_name="username"),
        "vpc_security_group_ids": sdk.FieldSpec(wire_name="vpc_security_group_ids"),
        "blue_green_update": sdk.FieldSpec(
            wire_name="blue_green_update",
            kind="list",
            fields={
                "enabled": sdk.FieldSpec(wire_name="enabled"),
            },
        ),
        "restore_to_point_in_time": sdk.FieldSpec(
            wire_name="restore_to_point_in_time",
            kind="list",
            fields={
                "restore_time": sdk.FieldSpec(wire_name="restore_time"),
                "source_db_instance_automated_backups_arn": sdk.FieldSpec(wire_name="source_db_instance_automated_backups_arn"),
                "source_db_instance_identifier": sdk.FieldSpec(wire_name="source_db_instance_identifier"),
                "source_dbi_resource_id": sdk.FieldSpec(wire_name="source_dbi_resource_id"),
                "use_latest_restorable_time": sdk.FieldSpec(wire_name="use_latest_restorable_time"),
            },
        ),
        "s3_import": sdk.FieldSpec(
            wire_name="s3_import",
            kind="list",
            fields={
                "bucket_name": sdk.FieldSpec(wire_name="bucket_name"),
                "bucket_prefix": sdk.FieldSpec(wire_name="bucket_prefix"),
                "ingestion_role": sdk.FieldSpec(wire_name="ingestion_role"),
                "source_engine": sdk.FieldSpec(wire_name="source_engine"),
                "source_engine_version": sdk.FieldSpec(wire_name="source_engine_version"),
            },
        ),
        "timeouts": sdk.FieldSpec(
            wire_name="timeouts",
            kind="object",
            fields={
                "create": sdk.FieldSpec(wire_name="create"),
                "delete": sdk.FieldSpec(wire_name="delete"),
                "update": sdk.FieldSpec(wire_name="update"),
            },
        ),
    },
)
