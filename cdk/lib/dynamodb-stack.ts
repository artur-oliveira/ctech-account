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

    // ── ctech_users ────────────────────────────────────────────────────────────
    const usersTable = new dynamodb.TableV2(this, 'UsersTable', {
      tableName: `${environment}_ctech_users`,
      partitionKey: {name: 'pk', type: dynamodb.AttributeType.STRING},
      billing: dynamodb.Billing.onDemand(),
      pointInTimeRecoverySpecification: {
        pointInTimeRecoveryEnabled: pitr,
      },
      removalPolicy,
      globalSecondaryIndexes: [
        {
          indexName: 'email-index',
          partitionKey: {name: 'email', type: dynamodb.AttributeType.STRING},
          projectionType: dynamodb.ProjectionType.ALL,
        },
      ],
    });
    this.tables.set('ctech_users', usersTable);

    // ── ctech_sessions ─────────────────────────────────────────────────────────
    const sessionsTable = new dynamodb.TableV2(this, 'SessionsTable', {
      tableName: `${environment}_ctech_sessions`,
      partitionKey: {name: 'pk', type: dynamodb.AttributeType.STRING},
      sortKey: {name: 'sk', type: dynamodb.AttributeType.STRING},
      billing: dynamodb.Billing.onDemand(),
      timeToLiveAttribute: 'expires_at',
      pointInTimeRecoverySpecification: {
        pointInTimeRecoveryEnabled: pitr,
      },
      removalPolicy,
    });
    this.tables.set('ctech_sessions', sessionsTable);

    // ── ctech_oauth_clients ────────────────────────────────────────────────────
    const oauthClientsTable = new dynamodb.TableV2(this, 'OAuthClientsTable', {
      tableName: `${environment}_ctech_oauth_clients`,
      partitionKey: {name: 'pk', type: dynamodb.AttributeType.STRING},
      billing: dynamodb.Billing.onDemand(),
      pointInTimeRecoverySpecification: {
        pointInTimeRecoveryEnabled: pitr,
      },
      removalPolicy,
      globalSecondaryIndexes: [
        {
          indexName: 'owner-index',
          partitionKey: {name: 'owner_user_id', type: dynamodb.AttributeType.STRING},
          projectionType: dynamodb.ProjectionType.ALL,
        },
      ],
    });
    this.tables.set('ctech_oauth_clients', oauthClientsTable);

    // ── ctech_api_keys ─────────────────────────────────────────────────────────
    const apiKeysTable = new dynamodb.TableV2(this, 'APIKeysTable', {
      tableName: `${environment}_ctech_api_keys`,
      partitionKey: {name: 'pk', type: dynamodb.AttributeType.STRING},
      sortKey: {name: 'sk', type: dynamodb.AttributeType.STRING},
      billing: dynamodb.Billing.onDemand(),
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
        },
      ],
    });
    this.tables.set('ctech_api_keys', apiKeysTable);

    // ── ctech_mfa ──────────────────────────────────────────────────────────────
    // Stores TOTP secrets (sk=TOTP_default) and PassKey credentials (sk=PASSKEY_{id})
    const mfaTable = new dynamodb.TableV2(this, 'MFATable', {
      tableName: `${environment}_ctech_mfa`,
      partitionKey: {name: 'pk', type: dynamodb.AttributeType.STRING},
      sortKey: {name: 'sk', type: dynamodb.AttributeType.STRING},
      billing: dynamodb.Billing.onDemand(),
      pointInTimeRecoverySpecification: {
        pointInTimeRecoveryEnabled: pitr,
      },
      removalPolicy,
    });
    this.tables.set('ctech_mfa', mfaTable);

    // ── Outputs ────────────────────────────────────────────────────────────────
    for (const [name, table] of this.tables) {
      new cdk.CfnOutput(this, `${name}-arn`, {
        value: table.tableArn,
        exportName: `${id}-${name}-arn`,
      });
    }
  }
}
