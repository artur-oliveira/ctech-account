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
  kycDocumentsBucketArn: string;
}

export class IAMStack extends cdk.Stack {
  public readonly instanceProfileName: string;

  constructor(scope: Construct, id: string, props: IAMStackProps) {
    super(scope, id, props);

    const {environment, dynamoDBTables, deploymentsBucketArn, logsBucketArn, kycDocumentsBucketArn} = props;

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
        'dynamodb:DescribeTable',
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

    // SSM — JWK auto-rotation writes its own versioned key parameters
    // (jwk/active + jwk/previous). Write access is scoped to that subtree only.
    appRole.addToPolicy(new iam.PolicyStatement({
      actions: ['ssm:PutParameter'],
      resources: [
        `arn:aws:ssm:*:*:parameter/ctech-account/${environment}/jwk/*`,
      ],
    }));

    // SES — verification / password-reset emails. Scoped to identities (FROM address
    // comes from SSM at runtime, so we allow any verified identity in the account).
    appRole.addToPolicy(new iam.PolicyStatement({
      actions: ['ses:SendEmail', 'ses:SendRawEmail'],
      resources: ['arn:aws:ses:*:*:identity/*'],
    }));

    // S3 — deployments (read) + logs (write), scoped to this app's prefix
    appRole.addToPolicy(new iam.PolicyStatement({
      actions: ['s3:GetObject'],
      resources: [`${deploymentsBucketArn}/ctech-account/*`],
    }));
    appRole.addToPolicy(new iam.PolicyStatement({
      actions: ['s3:PutObject'],
      resources: [`${logsBucketArn}/ctech-account/*`],
    }));

    // S3 — KYC identity documents. The API only ever presigns URLs and HEADs
    // objects to confirm an upload landed; it never streams the bytes itself.
    appRole.addToPolicy(new iam.PolicyStatement({
      actions: ['s3:PutObject', 's3:GetObject'],
      resources: [`${kycDocumentsBucketArn}/kyc/*`],
    }));

    // EC2 — update-realip.sh reads the AWS-managed CloudFront origin-facing
    // prefix list. Both actions are read-only and do not support resource-level
    // permissions, so Resource must be *.
    appRole.addToPolicy(new iam.PolicyStatement({
      actions: ['ec2:DescribeManagedPrefixLists', 'ec2:GetManagedPrefixListEntries'],
      resources: ['*'],
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
