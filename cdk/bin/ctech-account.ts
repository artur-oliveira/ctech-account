#!/usr/bin/env node
import * as cdk from 'aws-cdk-lib';

import {DynamoDBStack} from '../lib/dynamodb-stack';
import {ComputeStack} from '../lib/compute-stack';
import {FrontendStack} from '../lib/frontend-stack';
import {IAMStack} from '../lib/iam-stack';
import {OidcStack} from '../lib/oidc-stack';
import {S3Stack} from '../lib/s3-stack';
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
// S3 (deployments + logs — owned by this CDK)
// =====================
const s3Stack = new S3Stack(app, id('S3'), {
  env,
  environment: ENVIRONMENT,
  description: `ctech-account S3 (deployments + logs) - ${ENVIRONMENT}`,
});

// =====================
// IAM (instance profile for EC2)
// =====================
const iamStack = new IAMStack(app, id('IAM'), {
  env,
  environment: ENVIRONMENT,
  dynamoDBTables: dynamodbStack.tables,
  deploymentsBucketArn: s3Stack.deploymentsBucketArn,
  logsBucketArn: s3Stack.logsBucketArn,
  description: `ctech-account IAM - ${ENVIRONMENT}`,
});
iamStack.addDependency(dynamodbStack);
iamStack.addDependency(s3Stack);

// =====================
// Compute (EC2 + ASG, shared ALB from ctech-cdk)
// =====================
const computeStack = new ComputeStack(app, id('Compute'), {
  env,
  environment: ENVIRONMENT,
  vpcId: CTECH_VPC_ID,
  domainName: domainForEnv(ENVIRONMENT, 'accountsapi'), // accounts-api.arturocarvalho.com → ALB
  instanceProfileName: iamStack.instanceProfileName,
  deploymentsBucketName: s3Stack.deploymentsBucketName,
  logsBucketName: s3Stack.logsBucketName,
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
