import * as cdk from 'aws-cdk-lib';
import * as s3 from 'aws-cdk-lib/aws-s3';
import {Construct} from 'constructs';
import {Environment} from './types';

interface ExportsStackProps extends cdk.StackProps {
  environment: Environment;
  bucketName: string;
}

/**
 * Private bucket holding the personal-data export zips
 * (docs/specs/2026-10-17-data-export.md). The API writes them and mints
 * short-lived presigned GETs for the signed-in person; the browser never PUTs,
 * so there is no CORS rule. Objects expire on their own a day after the API
 * stops offering them (ReadyTTL is 7 days), so a forgotten export does not
 * keep personal data around. Not versioned: a deleted export must be gone.
 */
export class ExportsStack extends cdk.Stack {
  public readonly bucketName: string;
  public readonly bucketArn: string;

  constructor(scope: Construct, id: string, props: ExportsStackProps) {
    super(scope, id, props);

    const {environment, bucketName} = props;
    const isProd = environment === 'prod';

    const bucket = new s3.Bucket(this, 'DataExportsBucket', {
      bucketName,
      blockPublicAccess: s3.BlockPublicAccess.BLOCK_ALL,
      encryption: s3.BucketEncryption.S3_MANAGED,
      enforceSSL: true,
      // Short-lived copies, regenerable on request: safe to tear down with the stack.
      removalPolicy: cdk.RemovalPolicy.DESTROY,
      autoDeleteObjects: true,
      lifecycleRules: [{
        id: 'expire-data-exports',
        prefix: 'exports/',
        expiration: cdk.Duration.days(isProd ? 8 : 2),
        abortIncompleteMultipartUploadAfter: cdk.Duration.days(1),
      }],
    });

    this.bucketName = bucket.bucketName;
    this.bucketArn = bucket.bucketArn;

    new cdk.CfnOutput(this, 'DataExportsBucketName', {
      value: this.bucketName,
      exportName: `${id}-data-exports-bucket`,
    });
  }
}
