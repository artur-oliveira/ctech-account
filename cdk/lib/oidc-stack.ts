import * as cdk from 'aws-cdk-lib';
import * as iam from 'aws-cdk-lib/aws-iam';
import {Construct} from 'constructs';

interface OidcStackProps extends cdk.StackProps {
  githubRepo: string; // e.g. "artur-oliveira/ctech-account"
}

export class OidcStack extends cdk.Stack {
  constructor(scope: Construct, id: string, props: OidcStackProps) {
    super(scope, id, props);

    const {githubRepo} = props;

    // GitHub OIDC provider is a global IAM resource (one per account).
    // py-dfe-cdk owns its creation — import it here by its well-known ARN.
    const providerArn = `arn:aws:iam::${this.account}:oidc-provider/token.actions.githubusercontent.com`;
    const provider = iam.OpenIdConnectProvider.fromOpenIdConnectProviderArn(
      this, 'GithubOIDC', providerArn,
    );

    const trust = new iam.WebIdentityPrincipal(provider.openIdConnectProviderArn, {
      StringLike: {
        'token.actions.githubusercontent.com:sub': `repo:${githubRepo}:*`,
      },
      StringEquals: {
        'token.actions.githubusercontent.com:aud': 'sts.amazonaws.com',
      },
    });

    const deployRole = new iam.Role(this, 'GithubDeployRole', {
      roleName: 'ctech-account-github-deploy-role',
      assumedBy: trust,
      description: 'Role assumed by GitHub Actions for ctech-account deploys',
    });

    // S3 — upload artifacts to shared deployments bucket under ctech-account/ prefix
    deployRole.addToPolicy(new iam.PolicyStatement({
      actions: ['s3:ListBucket'],
      resources: ['arn:aws:s3:::*-ctech-deployments'],
      conditions: {StringLike: {'s3:prefix': 'ctech-account/*'}},
    }));
    deployRole.addToPolicy(new iam.PolicyStatement({
      actions: ['s3:PutObject', 's3:GetObject'],
      resources: ['arn:aws:s3:::*-ctech-deployments/ctech-account/*'],
    }));

    // S3 — sync frontend assets to S3 static hosting bucket
    deployRole.addToPolicy(new iam.PolicyStatement({
      actions: ['s3:ListBucket'],
      resources: ['arn:aws:s3:::*-ctech-account-frontend'],
    }));
    deployRole.addToPolicy(new iam.PolicyStatement({
      actions: ['s3:PutObject', 's3:GetObject', 's3:DeleteObject'],
      resources: ['arn:aws:s3:::*-ctech-account-frontend/*'],
    }));

    // SSM — read VPC ID and ALB listener ARN at synth time
    deployRole.addToPolicy(new iam.PolicyStatement({
      actions: ['ssm:GetParameter'],
      resources: ['arn:aws:ssm:*:*:parameter/ctech/*'],
    }));

    // ASG — describe instances for rolling deploy
    deployRole.addToPolicy(new iam.PolicyStatement({
      actions: [
        'autoscaling:DescribeAutoScalingGroups',
        'ec2:DescribeInstances',
      ],
      resources: ['*'],
    }));

    // SSM — send command to instances
    deployRole.addToPolicy(new iam.PolicyStatement({
      actions: [
        'ssm:SendCommand',
        'ssm:GetCommandInvocation',
        'ssm:ListCommandInvocations',
      ],
      resources: ['*'],
    }));

    // CloudFront — invalidate distribution
    deployRole.addToPolicy(new iam.PolicyStatement({
      actions: ['cloudfront:CreateInvalidation'],
      resources: ['*'],
    }));

    // Route manifest for the URL-rewrite CloudFront Function. Published after
    // the S3 sync so the key set matches the objects in the bucket.
    deployRole.addToPolicy(new iam.PolicyStatement({
      actions: [
        'cloudfront-keyvaluestore:DescribeKeyValueStore',
        'cloudfront-keyvaluestore:ListKeys',
        'cloudfront-keyvaluestore:UpdateKeys',
      ],
      resources: [`arn:aws:cloudfront::${this.account}:key-value-store/*`],
    }));

    // CDK deploy permissions
    deployRole.addToPolicy(new iam.PolicyStatement({
      actions: [
        'cloudformation:*',
        'sts:AssumeRole',
      ],
      resources: ['*'],
    }));

    // Infrastructure role — assumed by .github/workflows/infra.yml only.
    // `cdk deploy` assumes the CDK bootstrap roles and creates/updates every
    // resource type in this repo (DynamoDB, EC2, IAM, S3, CloudFront), so it
    // needs broad permissions. Mirrors ctech-wallet / ctech-dfe.
    const infraRole = new iam.Role(this, 'GithubInfraRole', {
      roleName: 'ctech-account-gha-infra',
      assumedBy: trust,
      description: 'Role assumed by GitHub Actions to run cdk deploy for ctech-account',
    });
    infraRole.addManagedPolicy(
      iam.ManagedPolicy.fromAwsManagedPolicyName('AdministratorAccess'),
    );

    new cdk.CfnOutput(this, 'DeployRoleArn', {
      value: deployRole.roleArn,
      exportName: 'ctech-account-github-deploy-role-arn',
    });

    new cdk.CfnOutput(this, 'InfraRoleArn', {value: infraRole.roleArn});
  }
}
