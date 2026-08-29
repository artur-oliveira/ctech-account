import * as cdk from 'aws-cdk-lib'
import {Match, Template} from 'aws-cdk-lib/assertions'
import {OidcStack} from '../lib/oidc-stack'

test('GitHub deploy role can run the deploy script only on account instances', () => {
  const app = new cdk.App()
  const stack = new OidcStack(app, 'TestOidcStack', {
    env: {account: '868899309401', region: 'us-east-1'},
    githubRepo: 'artur-oliveira/ctech-account',
  })
  const template = Template.fromStack(stack)
  const policy = Object.values(template.findResources('AWS::IAM::Policy'))[0] as {
    Properties: {PolicyDocument: {Statement: Array<Record<string, unknown>>}}
  }
  const statements = policy.Properties.PolicyDocument.Statement

  template.hasResourceProperties('AWS::IAM::Policy', {
    PolicyDocument: {
      Statement: Match.arrayWith([
        Match.objectLike({
          Action: 'ssm:SendCommand',
          Effect: 'Allow',
          Resource: {
            'Fn::Join': ['', ['arn:', {Ref: 'AWS::Partition'}, ':ssm:us-east-1::document/AWS-RunShellScript']],
          },
        }),
        Match.objectLike({
          Action: 'ssm:SendCommand',
          Effect: 'Allow',
          Resource: {
            'Fn::Join': ['', ['arn:', {Ref: 'AWS::Partition'}, ':ec2:us-east-1:868899309401:instance/*']],
          },
          Condition: {StringEquals: {'ssm:resourceTag/Project': 'ctech-account'}},
        }),
      ]),
    },
    Roles: Match.arrayWith([{Ref: Match.stringLikeRegexp('GithubDeployRole')}]),
  })

  expect(statements.some((statement) => {
    const actions = Array.isArray(statement.Action) ? statement.Action : [statement.Action]
    return actions.includes('ssm:GetCommandInvocation') && statement.Resource === '*'
  })).toBe(true)
  expect(statements.some((statement) => {
    const actions = Array.isArray(statement.Action) ? statement.Action : [statement.Action]
    return actions.includes('ssm:SendCommand') && statement.Resource === '*'
  })).toBe(false)
})
