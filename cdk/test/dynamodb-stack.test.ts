import * as cdk from 'aws-cdk-lib'
import {Match, Template} from 'aws-cdk-lib/assertions'
import {DynamoDBStack} from '../lib/dynamodb-stack'

test('support metrics table is on-demand with a string bucket key', () => {
  const app = new cdk.App()
  const stack = new DynamoDBStack(app, 'TestDynamoDBStack', {
    env: {account: '868899309401', region: 'us-east-1'},
    environment: 'prod',
  })
  const template = Template.fromStack(stack)
  template.hasResourceProperties('AWS::DynamoDB::GlobalTable', {
    TableName: 'prod_account_support_metrics',
    BillingMode: 'PAY_PER_REQUEST',
    KeySchema: [{AttributeName: 'pk', KeyType: 'HASH'}],
  })
})

test('users table exposes the KYC review queue index', () => {
  const app = new cdk.App()
  const stack = new DynamoDBStack(app, 'TestKYCReviewIndexStack', {
    env: {account: '868899309401', region: 'us-east-1'},
    environment: 'prod',
  })
  const template = Template.fromStack(stack)
  template.hasResourceProperties('AWS::DynamoDB::GlobalTable', {
    TableName: 'prod_account_users',
    GlobalSecondaryIndexes: Match.arrayWith([
      Match.objectLike({
        IndexName: 'kyc-level-index',
        KeySchema: [
          {AttributeName: 'kyc_level', KeyType: 'HASH'},
          {AttributeName: 'kyc_submitted_at', KeyType: 'RANGE'},
        ],
        Projection: {ProjectionType: 'ALL'},
      }),
    ]),
  })
})

test('users table expires KYC retention items via TTL', () => {
  const app = new cdk.App()
  const stack = new DynamoDBStack(app, 'TestUsersTTLStack', {
    env: {account: '868899309401', region: 'us-east-1'},
    environment: 'prod',
  })
  Template.fromStack(stack).hasResourceProperties('AWS::DynamoDB::GlobalTable', {
    TableName: 'prod_account_users',
    TimeToLiveSpecification: {AttributeName: 'expires_at', Enabled: true},
  })
})

test('data exports table keeps one CURRENT row per person', () => {
  const stack = new DynamoDBStack(new cdk.App(), 'TestExportsTable', {
    env: {account: '868899309401', region: 'us-east-1'},
    environment: 'prod',
  })
  Template.fromStack(stack).hasResourceProperties('AWS::DynamoDB::GlobalTable', {
    TableName: 'prod_account_data_exports',
    KeySchema: [{AttributeName: 'pk', KeyType: 'HASH'}, {AttributeName: 'sk', KeyType: 'RANGE'}],
  })
})

test('idempotency table is on-demand with pk/sk and TTL on ttl', () => {
  const app = new cdk.App()
  const stack = new DynamoDBStack(app, 'TestIdempotencyStack', {
    env: {account: '868899309401', region: 'us-east-1'},
    environment: 'prod',
  })
  Template.fromStack(stack).hasResourceProperties('AWS::DynamoDB::GlobalTable', {
    TableName: 'prod_account_idempotency',
    BillingMode: 'PAY_PER_REQUEST',
    KeySchema: [
      {AttributeName: 'pk', KeyType: 'HASH'},
      {AttributeName: 'sk', KeyType: 'RANGE'},
    ],
    TimeToLiveSpecification: {AttributeName: 'ttl', Enabled: true},
  })
})
