import * as cdk from 'aws-cdk-lib';
import * as s3 from 'aws-cdk-lib/aws-s3';
import {Construct} from 'constructs';
import {Environment} from './types';

// ui/ dev server always runs on this fixed port (see ui/package.json "dev" script).
// Allowed on every environment's bucket, including prod, so local frontend dev can
// exercise the presigned-upload flow against any deployed backend.
const LOCAL_DEV_ORIGIN = 'http://localhost:3001'; // TODO: remove this, test only

interface KYCStackProps extends cdk.StackProps {
  environment: Environment;
  bucketName: string;
  // Origin allowed to PUT documents — the browser uploads straight to S3 through a
  // presigned URL, so the bucket itself must accept the frontend's CORS preflight.
  frontendOrigin: string;
}

/**
 * Private bucket holding identity documents uploaded for manual KYC review.
 * Objects are only ever reached through presigned URLs minted by the API.
 */
export class KYCStack extends cdk.Stack {
  public readonly documentsBucketName: string;
  public readonly documentsBucketArn: string;

  constructor(scope: Construct, id: string, props: KYCStackProps) {
    super(scope, id, props);

    const {environment, bucketName, frontendOrigin} = props;
    const isProd = environment === 'prod';

    const documentsBucket = new s3.Bucket(this, 'KYCDocumentsBucket', {
      bucketName,
      blockPublicAccess: s3.BlockPublicAccess.BLOCK_ALL,
      encryption: s3.BucketEncryption.S3_MANAGED,
      enforceSSL: true,
      versioned: true,
      // Identity documents are regulated records — keep them out of reach of a
      // stack teardown in prod.
      removalPolicy: isProd ? cdk.RemovalPolicy.RETAIN : cdk.RemovalPolicy.DESTROY,
      autoDeleteObjects: !isProd,
      lifecycleRules: [{
        id: 'expire-identity-documents',
        expiration: cdk.Duration.days(5 * 365),
        noncurrentVersionExpiration: cdk.Duration.days(30),
      }],
      cors: [{
        allowedOrigins: [frontendOrigin, LOCAL_DEV_ORIGIN],
        allowedMethods: [s3.HttpMethods.PUT],
        allowedHeaders: ['content-type'],
        maxAge: 3000,
      }],
    });

    this.documentsBucketName = documentsBucket.bucketName;
    this.documentsBucketArn = documentsBucket.bucketArn;

    new cdk.CfnOutput(this, 'KYCDocumentsBucketName', {
      value: this.documentsBucketName,
      exportName: `${id}-kyc-documents-bucket`,
    });
  }
}
