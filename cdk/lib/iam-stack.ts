import * as cdk from 'aws-cdk-lib';
import * as iam from 'aws-cdk-lib/aws-iam';
import * as dynamodb from 'aws-cdk-lib/aws-dynamodb';
import {Construct} from 'constructs';
import {Environment} from './types';

interface IAMStackProps extends cdk.StackProps {
  environment: Environment;
  dynamoDBTables: Map<string, dynamodb.TableV2>;
  deploymentsBucketArn: string;
  logsBucketArn: string;
}

export class IAMStack extends cdk.Stack {
  public readonly instanceProfileName: string;

  constructor(scope: Construct, id: string, props: IAMStackProps) {
    super(scope, id, props);

    const {environment, dynamoDBTables, deploymentsBucketArn, logsBucketArn} = props;

    const appRole = new iam.Role(this, 'AppRole', {
      roleName: `${environment}-ctech-account-role`,
      assumedBy: new iam.ServicePrincipal('ec2.amazonaws.com'),
      managedPolicies: [
        iam.ManagedPolicy.fromAwsManagedPolicyName('AmazonSSMManagedInstanceCore'),
        iam.ManagedPolicy.fromAwsManagedPolicyName('CloudWatchAgentServerPolicy'),
      ],
    });

    // DynamoDB — all ctech_* tables
    appRole.addToPolicy(new iam.PolicyStatement({
      actions: [
        'dynamodb:GetItem',
        'dynamodb:PutItem',
        'dynamodb:UpdateItem',
        'dynamodb:DeleteItem',
        'dynamodb:Query',
        'dynamodb:BatchGetItem',
        'dynamodb:BatchWriteItem',
        'dynamodb:TransactWriteItems',
      ],
      resources: [...dynamoDBTables.values()].flatMap(t => [t.tableArn, `${t.tableArn}/index/*`]),
    }));

    // SSM — read secrets
    appRole.addToPolicy(new iam.PolicyStatement({
      actions: ['ssm:GetParameter'],
      resources: [
        `arn:aws:ssm:*:*:parameter/ctech-account/${environment}/*`,
        `arn:aws:ssm:*:*:parameter/ctech/${environment}/*`,
      ],
    }));

    // S3 — deployments (read) + logs (write)
    appRole.addToPolicy(new iam.PolicyStatement({
      actions: ['s3:GetObject'],
      resources: [`${deploymentsBucketArn}/*`],
    }));
    appRole.addToPolicy(new iam.PolicyStatement({
      actions: ['s3:PutObject'],
      resources: [`${logsBucketArn}/ctech-account/*`],
    }));

    this.instanceProfileName = `${environment}-ctech-account-instance-profile`;

    new iam.CfnInstanceProfile(this, 'InstanceProfile', {
      instanceProfileName: this.instanceProfileName,
      roles: [appRole.roleName],
    });

    new cdk.CfnOutput(this, 'InstanceProfileName', {
      value: this.instanceProfileName,
      exportName: `${id}-instance-profile-name`,
    });
  }
}
