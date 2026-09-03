import * as cdk from 'aws-cdk-lib'
import { Template } from 'aws-cdk-lib/assertions'
import { ApiStack } from '../lib/api-stack'

/** EC2's hard cap on user data, which a deploy discovers and not a review. */
const USER_DATA_LIMIT_BYTES = 16384

let cachedTemplate: Template | undefined

function synth(): Template {
  if (cachedTemplate) return cachedTemplate
  const app = new cdk.App()
  const stack = new ApiStack(app, 'TestComputeStack', {
    env: { account: '868899309401', region: 'us-east-1' },
    environment: 'prod',
    vpcId: 'vpc-0adfd86727d17445b',
    instanceProfileName: 'prod-ctech-account-instance-profile',
    deploymentsBucketName: 'prod-ctech-deployments',
    logsBucketName: 'prod-ctech-application-logs',
    kycDocumentsBucketName: 'prod-ctech-account-kyc',
    valkeyUrlSsmPath: '/ctech/prod/valkey/url',
  })
  cachedTemplate = Template.fromStack(stack)
  return cachedTemplate
}

function userDataText(): string {
  const template = synth()
  const launchTemplate = Object.values(template.findResources('AWS::EC2::LaunchTemplate'))[0] as any
  const encoded = launchTemplate.Properties.LaunchTemplateData.UserData['Fn::Base64']
  if (typeof encoded === 'string') return encoded
  return (encoded['Fn::Join'][1] as unknown[])
    .map((part) => (typeof part === 'string' ? part : '<<token>>'))
    .join('')
}

test('compute user data is script invocations, not inline files', () => {
  const text = userDataText()
  expect(text).toContain("ctech_run setup-nginx.sh '8080' '8000' '/v1.0/health-check' '20' '5m'")
  expect(text).toContain("ctech_run setup-app-service.sh 'CTech Account API' 'bootstrap'")
  // nginx.conf, start.sh, deploy.sh and upload-logs.sh are no longer inline.
  expect(text).not.toContain('limit_req_zone')
  expect(text).not.toContain('/opt/app/deploy.sh <')
})

test('user data stays well under the EC2 limit', () => {
  expect(Buffer.byteLength(userDataText(), 'utf8')).toBeLessThan(USER_DATA_LIMIT_BYTES)
})

test('nginx forwards the support websocket upgrade', () => {
  const text = userDataText()
  expect(text).toContain('location-support-ws.conf')
  expect(text).toContain('support/tickets/[^/]+/ws')
  expect(text).toContain('proxy_set_header Upgrade $http_upgrade')
  expect(text).toContain('proxy_read_timeout 3600s')
})

test('no secret value is written into the launch template', () => {
  // The instance reads secrets from SSM at service start, using its own role.
  const text = userDataText()
  expect(text).toContain("'SECRET_ENC_KEY=/ctech-account/prod/secret-encryption-key'")
  expect(text).not.toMatch(/SECRET_ENC_KEY=(?!\/|\$)/)
})

test('the Spot policy can launch both nano and micro Graviton instances', () => {
  synth().hasResourceProperties('AWS::AutoScaling::AutoScalingGroup', {
    MixedInstancesPolicy: {
      LaunchTemplate: {
        Overrides: [{ InstanceType: 't4g.nano' }, { InstanceType: 't4g.micro' }],
      },
    },
  })
})
