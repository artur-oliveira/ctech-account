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
