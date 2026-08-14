import * as cdk from 'aws-cdk-lib';
import * as cloudfront from 'aws-cdk-lib/aws-cloudfront';
import * as s3 from 'aws-cdk-lib/aws-s3';
import {createNextjsStaticFrontend} from '@aoctech/cdk';
import {Construct} from 'constructs';
import {Environment} from './types';

const API_PATH_PATTERNS = ['/v1.0/*', '/.well-known/*'];

interface FrontendStackProps extends cdk.StackProps {
  environment: Environment;
  certificateArn: string;
  domainName: string;
  apiDomainName: string;
  extraConnectSrc: string[];
}

export class FrontendStack extends cdk.Stack {
  public readonly bucket: s3.Bucket;
  public readonly distribution: cloudfront.Distribution;
  public readonly routeStore: cloudfront.KeyValueStore;

  constructor(scope: Construct, id: string, props: FrontendStackProps) {
    super(scope, id, props);

    const {bucket, distribution, routeStore} = createNextjsStaticFrontend(this, {
      environment: props.environment,
      serviceName: 'ctech-account',
      bucketName: `${props.environment}-ctech-account-frontend`,
      routeStoreName: `${props.environment}-ctech-account-routes`,
      apiDomainName: props.apiDomainName,
      apiPathPatterns: API_PATH_PATTERNS,
      connectSrc: [
        `https://${props.apiDomainName}`,
        ...props.extraConnectSrc.map((host) => `https://${host}`),
      ],
      domainName: props.domainName,
      certificateArn: props.certificateArn,
      distributionComment: `ctech-account Frontend - ${props.environment}`,
      securityHeadersPolicyName: `${props.environment}-CtechAccount-security-headers`,
      contentSecurityPolicyDirectives: [
        "default-src 'self'",
        "base-uri 'self'",
        "object-src 'none'",
        "frame-ancestors 'none'",
        "img-src 'self' data: blob:",
        "media-src 'self' blob:",
        "style-src 'self' 'unsafe-inline'",
        "script-src 'self' 'unsafe-inline'",
        `connect-src 'self' https://${props.apiDomainName} ${props.extraConnectSrc.map((host) => `https://${host}`).join(' ')}`.trim(),
      ],
      outputExportNamePrefix: id,
    });

    this.bucket = bucket;
    this.distribution = distribution;
    this.routeStore = routeStore;
  }
}
