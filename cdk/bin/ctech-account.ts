#!/usr/bin/env node
import * as cdk from 'aws-cdk-lib';

import {DynamoDBStack} from '../lib/dynamodb-stack';
import {ComputeStack} from '../lib/compute-stack';
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

const env = {account: AWS_ACCOUNT, region: AWS_REGION};

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
iamStack.addDependency(dynamodbStack);
iamStack.addDependency(kycStack);

// =====================
// Compute (EC2 + ASG, shared ALB from ctech-cdk)
// =====================
const computeStack = new ComputeStack(app, id('Compute'), {
    env,
    environment: ENVIRONMENT,
    vpcId: CTECH_VPC_ID,
    domainName: domainForEnv(ENVIRONMENT, 'accounts-api'), // accounts-api.aoctech.app → ALB
    instanceProfileName: iamStack.instanceProfileName,
    deploymentsBucketName: CTECH_DEPLOYMENTS_BUCKET,
    logsBucketName: CTECH_LOGS_BUCKET,
    kycDocumentsBucketName: KYC_DOCUMENTS_BUCKET,
    valkeyUrlSsmPath: `/ctech/${ENVIRONMENT}/valkey/url`,
    listenerRulePriority: 25, // py-dfe-api uses 15, ctech-wallet-api uses 35
    description: `ctech-account Compute (EC2 + ASG) - ${ENVIRONMENT}`,
});
computeStack.addDependency(iamStack);

// =====================
// Frontend (S3 + CloudFront)
// accounts.aoctech.app/             → UI (S3)
// accounts.aoctech.app/v1.0/*       → API (ALB) — browsers, same-origin, no CORS
// accounts.aoctech.app/.well-known/ → API (ALB) — OIDC discovery at the issuer host
// accounts-api.aoctech.app          → API (ALB) direct — service-to-service + public API
// =====================
new FrontendStack(app, id('Frontend'), {
    env,
    environment: ENVIRONMENT,
    certificateArn: CERT_ARN,
    domainName: domainForEnv(ENVIRONMENT, 'accounts'), // accounts.aoctech.app → CloudFront → S3
    apiDomainName: domainForEnv(ENVIRONMENT, 'accounts-api'),
    kycBucketDomain: `${KYC_DOCUMENTS_BUCKET}.s3.dualstack.${AWS_REGION}.amazonaws.com`,
    description: `ctech-account Frontend (S3 + CloudFront) - ${ENVIRONMENT}`,
});