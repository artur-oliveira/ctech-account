import * as cdk from 'aws-cdk-lib'
import {Match, Template} from 'aws-cdk-lib/assertions'
import {DynamoDBStack} from '../lib/dynamodb-stack'
import {IAMStack} from '../lib/iam-stack'

// The user-erasure topic is the trust anchor of the deletion saga: user.erase
// is irreversible, so only the account app role may publish to it.
test('user-erasure topic denies publish to every principal but the app role', () => {
  const app = new cdk.App()
  const env = {account: '868899309401', region: 'us-east-1'}
  const tables = new DynamoDBStack(app, 'TestIAMTables', {env, environment: 'prod'}).tables
  const stack = new IAMStack(app, 'TestIAMStack', {
    env,
    environment: 'prod',
    dynamoDBTables: tables,
    deploymentsBucketArn: 'arn:aws:s3:::deployments',
    logsBucketArn: 'arn:aws:s3:::logs',
    kycDocumentsBucketArn: 'arn:aws:s3:::kyc',
    exportsBucketArn: 'arn:aws:s3:::exports',
  })
  const template = Template.fromStack(stack)
  template.hasResourceProperties('AWS::SNS::TopicPolicy', {
    PolicyDocument: {
      Statement: Match.arrayWith([
        Match.objectLike({
          Effect: 'Deny',
          Action: 'sns:Publish',
          Principal: {AWS: '*'},
          Condition: {ArnNotEquals: {'aws:PrincipalArn': Match.anyValue()}},
        }),
      ]),
    },
  })
})

test('app role may list KYC objects and move them to retention', () => {
  const app = new cdk.App()
  const env = {account: '868899309401', region: 'us-east-1'}
  const tables = new DynamoDBStack(app, 'TestIAMTables2', {env, environment: 'prod'}).tables
  const stack = new IAMStack(app, 'TestIAMStack2', {
    env, environment: 'prod', dynamoDBTables: tables,
    deploymentsBucketArn: 'arn:aws:s3:::deployments', logsBucketArn: 'arn:aws:s3:::logs',
    kycDocumentsBucketArn: 'arn:aws:s3:::kyc',
    exportsBucketArn: 'arn:aws:s3:::exports',
  })
  Template.fromStack(stack).hasResourceProperties('AWS::IAM::Policy', {
    PolicyDocument: {Statement: Match.arrayWith([
      Match.objectLike({Action: 's3:ListBucket', Resource: 'arn:aws:s3:::kyc',
        Condition: {StringLike: {'s3:prefix': 'kyc/*'}}}),
      Match.objectLike({Action: 's3:PutObject', Resource: 'arn:aws:s3:::kyc/retention/*'}),
      Match.objectLike({Action: 's3:DeleteObject', Resource: 'arn:aws:s3:::kyc/kyc/*'}),
    ])},
  })
})

test('app role may write, read and delete data export objects only under exports/', () => {
  const app = new cdk.App()
  const env = {account: '868899309401', region: 'us-east-1'}
  const tables = new DynamoDBStack(app, 'TestExportTables', {env, environment: 'prod'}).tables
  const stack = new IAMStack(app, 'TestExportIAM', {
    env,
    environment: 'prod',
    dynamoDBTables: tables,
    deploymentsBucketArn: 'arn:aws:s3:::deployments',
    logsBucketArn: 'arn:aws:s3:::logs',
    kycDocumentsBucketArn: 'arn:aws:s3:::kyc',
    exportsBucketArn: 'arn:aws:s3:::exports',
  })
  Template.fromStack(stack).hasResourceProperties('AWS::IAM::Policy', {
    PolicyDocument: {
      Statement: Match.arrayWith([
        Match.objectLike({
          Action: ['s3:PutObject', 's3:GetObject', 's3:DeleteObject'],
          Resource: 'arn:aws:s3:::exports/exports/*',
        }),
      ]),
    },
  })
})
