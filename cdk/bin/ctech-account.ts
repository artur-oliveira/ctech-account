#!/usr/bin/env node
import * as cdk from 'aws-cdk-lib';

import {DynamoDBStack} from '../lib/dynamodb-stack';
import {ComputeStack} from '../lib/compute-stack';
import {FrontendStack} from '../lib/frontend-stack';
import {IAMStack} from '../lib/iam-stack';
import {OidcStack} from '../lib/oidc-stack';
import {Environment} from '../lib/types';

const app = new cdk.App();

// =====================
// Constants
// =====================
const AWS_ACCOUNT = '868899309401';
const AWS_REGION = 'us-east-1';
// Wildcard ACM cert — same as py-dfe (covers *.arturocarvalho.com)
const CERT_ARN = 'arn:aws:acm:us-east-1:868899309401:certificate/eb8aa9cd-f7c0-4c5a-bdbe-a25c4d4b20a5';

const ENVIRONMENT = (process.env.ENVIRONMENT || 'dev') as Environment;
const GITHUB_REPO = process.env.GITHUB_REPO || 'artur-oliveira/ctech-account';
const CTECH_VPC_ID = process.env.CTECH_VPC_ID || 'vpc-0adfd86727d17445b';
// Shared S3 buckets owned by ctech-cdk. CI reads these from SSM
// (/ctech/{env}/s3/deployments-bucket and /ctech/{env}/s3/logs-bucket)
// and sets them as env vars before running cdk deploy.
const CTECH_DEPLOYMENTS_BUCKET = process.env.CTECH_DEPLOYMENTS_BUCKET || `${ENVIRONMENT}-ctech-deployments`;
const CTECH_LOGS_BUCKET = process.env.CTECH_LOGS_BUCKET || `${ENVIRONMENT}-ctech-application-logs`;

const env = {account: AWS_ACCOUNT, region: AWS_REGION};

const BASE_DOMAIN = 'arturocarvalho.com';

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
// IAM (instance profile for EC2)
// Shared S3 bucket ARNs are derived from the bucket names read via env vars.
// =====================
const iamStack = new IAMStack(app, id('IAM'), {
  env,
  environment: ENVIRONMENT,
  dynamoDBTables: dynamodbStack.tables,
  deploymentsBucketArn: `arn:aws:s3:::${CTECH_DEPLOYMENTS_BUCKET}`,
  logsBucketArn: `arn:aws:s3:::${CTECH_LOGS_BUCKET}`,
  description: `ctech-account IAM - ${ENVIRONMENT}`,
});
iamStack.addDependency(dynamodbStack);

// =====================
// Compute (EC2 + ASG, shared ALB from ctech-cdk)
// =====================
const computeStack = new ComputeStack(app, id('Compute'), {
  env,
  environment: ENVIRONMENT,
  vpcId: CTECH_VPC_ID,
  domainName: domainForEnv(ENVIRONMENT, 'accountsapi'), // accounts-api.arturocarvalho.com → ALB
  instanceProfileName: iamStack.instanceProfileName,
  deploymentsBucketName: CTECH_DEPLOYMENTS_BUCKET,
  logsBucketName: CTECH_LOGS_BUCKET,
  valkeyUrlSsmPath: `/ctech/${ENVIRONMENT}/valkey/url`,
  listenerRulePriority: 20, // py-dfe-api uses 10
  description: `ctech-account Compute (EC2 + ASG) - ${ENVIRONMENT}`,
});
computeStack.addDependency(iamStack);

// =====================
// Frontend (S3 + CloudFront)
// accounts.arturocarvalho.com → UI (S3)
// accounts-api.arturocarvalho.com → API (ALB via compute stack)
// =====================
new FrontendStack(app, id('Frontend'), {
  env,
  environment: ENVIRONMENT,
  certificateArn: CERT_ARN,
  domainName: domainForEnv(ENVIRONMENT, 'accounts'), // accounts.arturocarvalho.com → CloudFront → S3
  description: `ctech-account Frontend (S3 + CloudFront) - ${ENVIRONMENT}`,
});
