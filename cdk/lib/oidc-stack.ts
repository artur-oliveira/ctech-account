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

    // GitHub OIDC provider — may already exist from py-dfe; use fromExisting if so.
    const provider = new iam.OpenIdConnectProvider(this, 'GithubOIDC', {
      url: 'https://token.actions.githubusercontent.com',
      clientIds: ['sts.amazonaws.com'],
    });

    const deployRole = new iam.Role(this, 'GithubDeployRole', {
      roleName: 'ctech-account-github-deploy-role',
      assumedBy: new iam.WebIdentityPrincipal(provider.openIdConnectProviderArn, {
        StringLike: {
          'token.actions.githubusercontent.com:sub': `repo:${githubRepo}:*`,
        },
        StringEquals: {
          'token.actions.githubusercontent.com:aud': 'sts.amazonaws.com',
        },
      }),
      description: 'Role assumed by GitHub Actions for ctech-account deploys',
    });

    // S3 — upload artifacts
    deployRole.addToPolicy(new iam.PolicyStatement({
      actions: ['s3:PutObject', 's3:GetObject', 's3:ListBucket'],
      resources: [
        'arn:aws:s3:::*-ctech-deployments',
        'arn:aws:s3:::*-ctech-deployments/*',
        // Also allow to the shared py-dfe deployments bucket if needed
        'arn:aws:s3:::*-py-dfe-deployments',
        'arn:aws:s3:::*-py-dfe-deployments/*',
      ],
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

    // CDK deploy permissions
    deployRole.addToPolicy(new iam.PolicyStatement({
      actions: [
        'cloudformation:*',
        'sts:AssumeRole',
      ],
      resources: ['*'],
    }));

    new cdk.CfnOutput(this, 'DeployRoleArn', {
      value: deployRole.roleArn,
      exportName: 'ctech-account-github-deploy-role-arn',
    });
  }
}
