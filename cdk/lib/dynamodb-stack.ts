import * as cdk from 'aws-cdk-lib';
import * as dynamodb from 'aws-cdk-lib/aws-dynamodb';
import {Construct} from 'constructs';
import {Environment} from './types';

interface DynamoDBStackProps extends cdk.StackProps {
  environment: Environment;
}

export class DynamoDBStack extends cdk.Stack {
  public readonly tables: Map<string, dynamodb.TableV2>;

  constructor(scope: Construct, id: string, props: DynamoDBStackProps) {
    super(scope, id, props);

    const {environment} = props;
    const isProd = environment === 'prod';

    this.tables = new Map();

    const removalPolicy = isProd ? cdk.RemovalPolicy.RETAIN : cdk.RemovalPolicy.DESTROY;
    const pitr = isProd;

    const usersTable = new dynamodb.TableV2(this, 'UsersTableV2', {
      tableName: `${environment}_account_users`,
      partitionKey: {name: 'pk', type: dynamodb.AttributeType.STRING},
      billing: dynamodb.Billing.onDemand({
        maxReadRequestUnits: 1000,
        maxWriteRequestUnits: 1000,
      }),
      pointInTimeRecoverySpecification: {
        pointInTimeRecoveryEnabled: pitr,
      },
      removalPolicy,
      globalSecondaryIndexes: [
        {
          indexName: 'email-index',
          partitionKey: {name: 'email', type: dynamodb.AttributeType.STRING},
          projectionType: dynamodb.ProjectionType.ALL,
          warmThroughput: undefined,
          maxReadRequestUnits: 1000,
          maxWriteRequestUnits: 1000,
        },
      ],
    });
    this.tables.set('account_users', usersTable);

    const sessionsTable = new dynamodb.TableV2(this, 'SessionsTableV2', {
      tableName: `${environment}_account_sessions`,
      partitionKey: {name: 'pk', type: dynamodb.AttributeType.STRING},
      sortKey: {name: 'sk', type: dynamodb.AttributeType.STRING},
      billing: dynamodb.Billing.onDemand({
        maxReadRequestUnits: 1000,
        maxWriteRequestUnits: 1000,
      }),
      timeToLiveAttribute: 'expires_at',
      pointInTimeRecoverySpecification: {
        pointInTimeRecoveryEnabled: pitr,
      },
      removalPolicy,
      globalSecondaryIndexes: [
        {
          indexName: 'token-hash-index',
          partitionKey: {name: 'refresh_token_hash', type: dynamodb.AttributeType.STRING},
          projectionType: dynamodb.ProjectionType.ALL,
          warmThroughput: undefined,
          maxReadRequestUnits: 1000,
          maxWriteRequestUnits: 1000,
        },
      ],
    });
    this.tables.set('account_sessions', sessionsTable);

    const oauthClientsTable = new dynamodb.TableV2(this, 'OAuthClientsTableV2', {
      tableName: `${environment}_account_oauth_clients`,
      partitionKey: {name: 'pk', type: dynamodb.AttributeType.STRING},
      billing: dynamodb.Billing.onDemand({
        maxReadRequestUnits: 1000,
        maxWriteRequestUnits: 1000,
      }),
      pointInTimeRecoverySpecification: {
        pointInTimeRecoveryEnabled: pitr,
      },
      removalPolicy,
      globalSecondaryIndexes: [
        {
          indexName: 'owner-index',
          partitionKey: {name: 'owner_user_id', type: dynamodb.AttributeType.STRING},
          projectionType: dynamodb.ProjectionType.ALL,
          warmThroughput: undefined,
          maxReadRequestUnits: 1000,
          maxWriteRequestUnits: 1000,
        },
      ],
    });
    this.tables.set('account_oauth_clients', oauthClientsTable);

    const apiKeysTable = new dynamodb.TableV2(this, 'APIKeysTableV2', {
      tableName: `${environment}_account_api_keys`,
      partitionKey: {name: 'pk', type: dynamodb.AttributeType.STRING},
      sortKey: {name: 'sk', type: dynamodb.AttributeType.STRING},
      billing: dynamodb.Billing.onDemand({
        maxReadRequestUnits: 1000,
        maxWriteRequestUnits: 1000,
      }),
      timeToLiveAttribute: 'expires_at',
      pointInTimeRecoverySpecification: {
        pointInTimeRecoveryEnabled: pitr,
      },
      removalPolicy,
      globalSecondaryIndexes: [
        {
          indexName: 'key-hash-index',
          partitionKey: {name: 'key_hash', type: dynamodb.AttributeType.STRING},
          projectionType: dynamodb.ProjectionType.ALL,
          warmThroughput: undefined,
          maxReadRequestUnits: 1000,
          maxWriteRequestUnits: 1000,
        },
      ],
    });
    this.tables.set('account_api_keys', apiKeysTable);

    // Stores TOTP secrets (sk=TOTP_default) and PassKey credentials (sk=PASSKEY_{id})
    const mfaTable = new dynamodb.TableV2(this, 'MFATableV2', {
      tableName: `${environment}_account_mfa`,
      partitionKey: {name: 'pk', type: dynamodb.AttributeType.STRING},
      sortKey: {name: 'sk', type: dynamodb.AttributeType.STRING},
      billing: dynamodb.Billing.onDemand({
        maxReadRequestUnits: 1000,
        maxWriteRequestUnits: 1000,
      }),
      pointInTimeRecoverySpecification: {
        pointInTimeRecoveryEnabled: pitr,
      },
      removalPolicy,
    });
    this.tables.set('account_mfa', mfaTable);

    const passkeysTable = new dynamodb.TableV2(this, 'PassKeyTableV2', {
      tableName: `${environment}_account_passkeys`,
      partitionKey: {name: 'pk', type: dynamodb.AttributeType.STRING},
      sortKey: {name: 'sk', type: dynamodb.AttributeType.STRING},
      billing: dynamodb.Billing.onDemand({
        maxReadRequestUnits: 1000,
        maxWriteRequestUnits: 1000,
      }),
      pointInTimeRecoverySpecification: {
        pointInTimeRecoveryEnabled: pitr,
      },
      removalPolicy,
    });
    this.tables.set('account_passkeys', passkeysTable);

    // Append-only security audit trail (pk=USER_{id}|ANON_{ip}, sk=EVT_{ts}_{rand}).
    // Events expire via TTL after 400 days.
    const auditTable = new dynamodb.TableV2(this, 'AuditTableV2', {
      tableName: `${environment}_account_audit`,
      partitionKey: {name: 'pk', type: dynamodb.AttributeType.STRING},
      sortKey: {name: 'sk', type: dynamodb.AttributeType.STRING},
      billing: dynamodb.Billing.onDemand({
        maxReadRequestUnits: 1000,
        maxWriteRequestUnits: 1000,
      }),
      timeToLiveAttribute: 'expires_at',
      pointInTimeRecoverySpecification: {
        pointInTimeRecoveryEnabled: pitr,
      },
      removalPolicy,
    });
    this.tables.set('account_audit', auditTable);

    // Platform-wide scope catalog — shared by every ctech service, hence the
    // {env}_ctech_scopes name instead of the {env}_account_* convention.
    // Single partition (pk=SERVICE, sk=<service code>) so one Query loads it all.
    const scopesTable = new dynamodb.TableV2(this, 'ScopesTableV2', {
      tableName: `${environment}_ctech_scopes`,
      partitionKey: {name: 'pk', type: dynamodb.AttributeType.STRING},
      sortKey: {name: 'sk', type: dynamodb.AttributeType.STRING},
      billing: dynamodb.Billing.onDemand({
        maxReadRequestUnits: 1000,
        maxWriteRequestUnits: 1000,
      }),
      pointInTimeRecoverySpecification: {
        pointInTimeRecoveryEnabled: pitr,
      },
      removalPolicy,
    });
    this.tables.set('ctech_scopes', scopesTable);

    for (const [name, table] of this.tables) {
      new cdk.CfnOutput(this, `${name}_TableName`, {
        value: table.tableName,
        exportName: `${id}-${name.replace(/_/g, '-')}`,
      });
    }
  }
}
