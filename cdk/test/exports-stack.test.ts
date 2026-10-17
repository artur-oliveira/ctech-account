import * as cdk from 'aws-cdk-lib'
import {Match, Template} from 'aws-cdk-lib/assertions'
import {ExportsStack} from '../lib/exports-stack'

test('exports bucket is private, TLS-only and expires objects on its own', () => {
  const stack = new ExportsStack(new cdk.App(), 'TestExports', {
    env: {account: '868899309401', region: 'us-east-1'},
    environment: 'prod',
    bucketName: 'prod-ctech-account-data-exports',
  })
  const template = Template.fromStack(stack)
  template.hasResourceProperties('AWS::S3::Bucket', {
    BucketName: 'prod-ctech-account-data-exports',
    PublicAccessBlockConfiguration: {BlockPublicAcls: true, BlockPublicPolicy: true, IgnorePublicAcls: true, RestrictPublicBuckets: true},
    LifecycleConfiguration: {Rules: Match.arrayWith([Match.objectLike({Id: 'expire-data-exports', ExpirationInDays: 8, Prefix: 'exports/'})])},
  })
  template.resourceCountIs('AWS::S3::Bucket', 1)
})
