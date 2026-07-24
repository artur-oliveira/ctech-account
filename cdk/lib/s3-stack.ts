import * as cdk from 'aws-cdk-lib';
import * as s3 from 'aws-cdk-lib/aws-s3';
import {Construct} from 'constructs';
import {Environment} from './types';

interface S3StackProps extends cdk.StackProps {
  environment: Environment;
}

export class S3Stack extends cdk.Stack {
  public readonly deploymentsBucketName: string;
  public readonly deploymentsBucketArn: string;
  public readonly logsBucketName: string;
  public readonly logsBucketArn: string;

  constructor(scope: Construct, id: string, props: S3StackProps) {
    super(scope, id, props);

    const {environment} = props;
    const isProd = environment === 'prod';

    const deploymentsBucket = new s3.Bucket(this, 'DeploymentsBucket', {
      bucketName: `${environment}-ctech-account-deployments`,
      blockPublicAccess: s3.BlockPublicAccess.BLOCK_ALL,
      encryption: s3.BucketEncryption.S3_MANAGED,
      removalPolicy: cdk.RemovalPolicy.DESTROY,
      autoDeleteObjects: true,
      lifecycleRules: [{expiration: cdk.Duration.days(30)}],
    });

    const logsBucket = new s3.Bucket(this, 'LogsBucket', {
      bucketName: `${environment}-ctech-account-logs`,
      blockPublicAccess: s3.BlockPublicAccess.BLOCK_ALL,
      encryption: s3.BucketEncryption.S3_MANAGED,
      removalPolicy: isProd ? cdk.RemovalPolicy.RETAIN : cdk.RemovalPolicy.DESTROY,
      autoDeleteObjects: !isProd,
    });

    this.deploymentsBucketName = deploymentsBucket.bucketName;
    this.deploymentsBucketArn = deploymentsBucket.bucketArn;
    this.logsBucketName = logsBucket.bucketName;
    this.logsBucketArn = logsBucket.bucketArn;

    new cdk.CfnOutput(this, 'DeploymentsBucketName', {
      value: this.deploymentsBucketName,
      exportName: `${id}-deployments-bucket`,
    });
    new cdk.CfnOutput(this, 'LogsBucketName', {
      value: this.logsBucketName,
      exportName: `${id}-logs-bucket`,
    });
  }
}
