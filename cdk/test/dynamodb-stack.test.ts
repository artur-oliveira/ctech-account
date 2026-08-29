import * as cdk from 'aws-cdk-lib'
import {Template} from 'aws-cdk-lib/assertions'
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
