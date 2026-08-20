#!/usr/bin/env node
import * as cdk from 'aws-cdk-lib';

import {DynamoDBStack} from '../lib/dynamodb-stack';
import {ApiStack} from '../lib/api-stack';
import {FrontendStack} from '../lib/frontend-stack';
import {IAMStack} from '../lib/iam-stack';
import {KYCStack} from '../lib/kyc-stack';
import {OidcStack} from '../lib/oidc-stack';
import {Environment} from '../lib/types';

const app = new cdk.App();

// =====================
// Constants
// =====================
const AWS_ACCOUNT = '868899309401';
const AWS_REGION = 'us-east-1';
// Wildcard ACM cert — same as py-dfe (covers *.aoctech.app)
const CERT_ARN = 'arn:aws:acm:us-east-1:868899309401:certificate/29678869-bfc3-4688-b81b-55aa5b1d7443';

const ENVIRONMENT = (process.env.ENVIRONMENT || 'dev') as Environment;
const GITHUB_REPO = process.env.GITHUB_REPO || 'artur-oliveira/ctech-account';
const CTECH_VPC_ID = process.env.CTECH_VPC_ID || 'vpc-0adfd86727d17445b';
// Shared S3 buckets owned by ctech-cdk. CI reads these from SSM
// (/ctech/{env}/s3/deployments-bucket and /ctech/{env}/s3/logs-bucket)
// and sets them as env vars before running cdk deploy.
const CTECH_DEPLOYMENTS_BUCKET = process.env.CTECH_DEPLOYMENTS_BUCKET || `${ENVIRONMENT}-ctech-deployments`;
const CTECH_LOGS_BUCKET = process.env.CTECH_LOGS_BUCKET || `${ENVIRONMENT}-ctech-application-logs`;
// KYC identity documents — owned by this repo (unlike the shared buckets above).
const KYC_DOCUMENTS_BUCKET = `${ENVIRONMENT}-ctech-account-kyc-documents`;
// Session Manager on the API instances. **Off by default**: deploys replace the
// instances through an ASG instance refresh, so nothing needs RunCommand any
// more, and the agent costs ~70 MiB of RSS on a t4g.nano. Set
// ENABLE_SSM_AGENT=true to get a shell back onto the box for debugging.
const ENABLE_SSM_AGENT = process.env.ENABLE_SSM_AGENT === 'true';

const env = {account: AWS_ACCOUNT, region: AWS_REGION};

// Cost allocation tags — applied to every resource in every stack.
// Requires manual activation as a cost allocation tag in the Billing console
// (Billing > Cost Allocation Tags) before it appears as a Cost Explorer group-by key.
cdk.Tags.of(app).add('Project', 'ctech-account');
cdk.Tags.of(app).add('Environment', ENVIRONMENT);

const BASE_DOMAIN = 'aoctech.app';

const domainForEnv = (environment: Environment, prefix: string) => {
  switch (environment) {
    case 'prod':
      return `${prefix}.${BASE_DOMAIN}`;
    case 'dev':
    case 'stage':
      return `${prefix}-${environment}.${BASE_DOMAIN}`;
  }
};

const id = (name: string) =>
  `CtechAccount-${ENVIRONMENT.charAt(0).toUpperCase() + ENVIRONMENT.slice(1)}-${name}`;

// =====================
// GitHub Actions OIDC (global, deployed once)
// =====================
new OidcStack(app, 'CtechAccount-Global-OIDC', {
  env,
  githubRepo: GITHUB_REPO,
  description: 'ctech-account GitHub Actions OIDC provider and deployment role (global)',
});

// =====================
// DynamoDB
// =====================
const dynamodbStack = new DynamoDBStack(app, id('DynamoDB'), {
  env,
  environment: ENVIRONMENT,
  description: `ctech-account DynamoDB - ${ENVIRONMENT}`,
});

// =====================
// KYC identity documents (private S3 bucket, presigned uploads)
// =====================
const kycStack = new KYCStack(app, id('KYC'), {
  env,
  environment: ENVIRONMENT,
  bucketName: KYC_DOCUMENTS_BUCKET,
  frontendOrigin: `https://${domainForEnv(ENVIRONMENT, 'accounts')}`,
  description: `ctech-account KYC documents bucket - ${ENVIRONMENT}`,
});

// =====================
// IAM (instance profile for EC2)
// Shared S3 bucket ARNs are derived from the bucket names read via env vars.
// =====================
const iamStack = new IAMStack(app, id('IAM'), {
  env,
  environment: ENVIRONMENT,
  dynamoDBTables: dynamodbStack.tables,
  deploymentsBucketArn: `arn:aws:s3:::${CTECH_DEPLOYMENTS_BUCKET}`,
  logsBucketArn: `arn:aws:s3:::${CTECH_LOGS_BUCKET}`,
  kycDocumentsBucketArn: `arn:aws:s3:::${KYC_DOCUMENTS_BUCKET}`,
  description: `ctech-account IAM - ${ENVIRONMENT}`,
});
iamStack.addStackDependency(dynamodbStack);
iamStack.addStackDependency(kycStack);

// =====================
// Compute (EC2 + ASG, routed by ctech-lbalancer HAProxy)
// =====================
const apiStack = new ApiStack(app, id('Api'), {
  env,
  environment: ENVIRONMENT,
  vpcId: CTECH_VPC_ID,
  instanceProfileName: iamStack.instanceProfileName,
  deploymentsBucketName: CTECH_DEPLOYMENTS_BUCKET,
  logsBucketName: CTECH_LOGS_BUCKET,
  kycDocumentsBucketName: KYC_DOCUMENTS_BUCKET,
  valkeyUrlSsmPath: `/ctech/${ENVIRONMENT}/valkey/url`,
  enableSsmAgent: ENABLE_SSM_AGENT,
  description: `ctech-account Compute (EC2 + ASG) - ${ENVIRONMENT}`,
});
apiStack.addStackDependency(iamStack);

// =====================
// Frontend (S3 + CloudFront)
// accounts.aoctech.app/             → UI (S3)
// accounts.aoctech.app/v1.0/*       → API (HAProxy) — browsers, same-origin, no CORS
// accounts.aoctech.app/.well-known/ → API (HAProxy) — OIDC discovery at the issuer host
// accounts-api.aoctech.app          → API (HAProxy) direct — service-to-service + public API
// =====================
new FrontendStack(app, id('Frontend'), {
  env,
  environment: ENVIRONMENT,
  certificateArn: CERT_ARN,
  domainName: domainForEnv(ENVIRONMENT, 'accounts'), // accounts.aoctech.app → CloudFront → S3
  apiDomainName: domainForEnv(ENVIRONMENT, 'accounts-api'),
  extraConnectSrc: [
    'viacep.com.br',
    `${KYC_DOCUMENTS_BUCKET}.s3.${AWS_REGION}.amazonaws.com`,
    `${KYC_DOCUMENTS_BUCKET}.s3.dualstack.${AWS_REGION}.amazonaws.com`,
  ],
  description: `ctech-account Frontend (S3 + CloudFront) - ${ENVIRONMENT}`,
});
