import * as cdk from 'aws-cdk-lib';
import * as s3 from 'aws-cdk-lib/aws-s3';
import * as cloudfront from 'aws-cdk-lib/aws-cloudfront';
import * as origins from 'aws-cdk-lib/aws-cloudfront-origins';
import * as acm from 'aws-cdk-lib/aws-certificatemanager';
import {GeoRestriction} from 'aws-cdk-lib/aws-cloudfront';
import {Construct} from 'constructs';
import {Environment} from './types';

interface FrontendStackProps extends cdk.StackProps {
  environment: Environment;
  certificateArn: string;
  domainName: string;       // e.g. accounts.arturocarvalho.com
  // ALB domain for API routing (the ALB DNS name, not accounts domain)
  albDnsName?: string;
}

export class FrontendStack extends cdk.Stack {
  public readonly bucket: s3.Bucket;
  public readonly distribution: cloudfront.Distribution;

  constructor(scope: Construct, id: string, props: FrontendStackProps) {
    super(scope, id, props);

    const {environment, certificateArn, domainName, albDnsName} = props;
    const isProd = environment === 'prod';

    this.bucket = new s3.Bucket(this, 'Bucket', {
      bucketName: `${environment}-ctech-account-frontend`,
      blockPublicAccess: s3.BlockPublicAccess.BLOCK_ALL,
      encryption: s3.BucketEncryption.S3_MANAGED,
      versioned: isProd,
      removalPolicy: isProd ? cdk.RemovalPolicy.RETAIN : cdk.RemovalPolicy.DESTROY,
      autoDeleteObjects: !isProd,
    });

    const oac = new cloudfront.S3OriginAccessControl(this, 'OAC', {
      originAccessControlName: `${environment}-ctech-account-oac`,
    });

    // Rewrite clean URLs to .html for Next.js static export
    const urlRewrite = new cloudfront.Function(this, 'UrlRewrite', {
      functionName: `${environment}-ctech-account-url-rewrite`,
      code: cloudfront.FunctionCode.fromInline(`
function handler(event) {
  var uri = event.request.uri;
  // Pass API and well-known paths through to the origin (ALB handles them)
  if (uri.startsWith('/v1/') || uri.startsWith('/.well-known/') || uri === '/health') {
    return event.request;
  }
  // Rewrite clean URLs to .html
  if (uri !== '/' && !/\\.[^/]+$/.test(uri)) {
    event.request.uri = uri.endsWith('/') ? uri + 'index.html' : uri + '.html';
  }
  return event.request;
}
      `),
      runtime: cloudfront.FunctionRuntime.JS_2_0,
    });

    const s3Origin = origins.S3BucketOrigin.withOriginAccessControl(this.bucket, {
      originAccessControl: oac,
    });

    const behaviors: cloudfront.AddBehaviorOptions = {
      viewerProtocolPolicy: cloudfront.ViewerProtocolPolicy.REDIRECT_TO_HTTPS,
      cachePolicy: cloudfront.CachePolicy.CACHING_OPTIMIZED,
      allowedMethods: cloudfront.AllowedMethods.ALLOW_GET_HEAD_OPTIONS,
      compress: true,
      functionAssociations: [{
        function: urlRewrite,
        eventType: cloudfront.FunctionEventType.VIEWER_REQUEST,
      }],
    };

    const additionalBehaviors: Record<string, cloudfront.BehaviorOptions> = {};

    // Route API calls and OIDC discovery to ALB (if provided)
    if (albDnsName) {
      const albOrigin = new origins.HttpOrigin(albDnsName, {
        protocolPolicy: cloudfront.OriginProtocolPolicy.HTTPS_ONLY,
      });
      const apiCachePolicy = new cloudfront.CachePolicy(this, 'ApiCachePolicy', {
        cachePolicyName: `${environment}-ctech-account-api-no-cache`,
        defaultTtl: cdk.Duration.seconds(0),
        maxTtl: cdk.Duration.seconds(0),
        minTtl: cdk.Duration.seconds(0),
        headerBehavior: cloudfront.CacheHeaderBehavior.allowList(
          'Authorization', 'Content-Type', 'Cookie',
        ),
        queryStringBehavior: cloudfront.CacheQueryStringBehavior.all(),
        cookieBehavior: cloudfront.CacheCookieBehavior.all(),
      });
      const apiOriginPolicy = cloudfront.OriginRequestPolicy.ALL_VIEWER_EXCEPT_HOST_HEADER;
      const apiBehavior: cloudfront.BehaviorOptions = {
        origin: albOrigin,
        viewerProtocolPolicy: cloudfront.ViewerProtocolPolicy.HTTPS_ONLY,
        cachePolicy: apiCachePolicy,
        originRequestPolicy: apiOriginPolicy,
        allowedMethods: cloudfront.AllowedMethods.ALLOW_ALL,
        compress: false,
      };
      additionalBehaviors['/v1/*'] = apiBehavior;
      additionalBehaviors['/.well-known/*'] = apiBehavior;
      additionalBehaviors['/health'] = apiBehavior;
    }

    this.distribution = new cloudfront.Distribution(this, 'Distribution', {
      comment: `ctech-account Frontend - ${environment}`,
      defaultBehavior: {...behaviors, origin: s3Origin},
      additionalBehaviors,
      defaultRootObject: 'index.html',
      errorResponses: [
        {httpStatus: 403, responseHttpStatus: 404, responsePagePath: '/404.html', ttl: cdk.Duration.seconds(0)},
        {httpStatus: 404, responseHttpStatus: 404, responsePagePath: '/404.html', ttl: cdk.Duration.seconds(0)},
      ],
      certificate: acm.Certificate.fromCertificateArn(this, 'Cert', certificateArn),
      domainNames: [domainName],
      priceClass: cloudfront.PriceClass.PRICE_CLASS_100,
      geoRestriction: GeoRestriction.allowlist('BR'),
      minimumProtocolVersion: cloudfront.SecurityPolicyProtocol.TLS_V1_2_2021,
    });

    new cdk.CfnOutput(this, 'BucketName', {value: this.bucket.bucketName, exportName: `${id}-bucket-name`});
    new cdk.CfnOutput(this, 'DistributionId', {value: this.distribution.distributionId, exportName: `${id}-dist-id`});
    new cdk.CfnOutput(this, 'DistributionDomain', {
      value: this.distribution.distributionDomainName,
      exportName: `${id}-dist-domain`
    });
  }
}
