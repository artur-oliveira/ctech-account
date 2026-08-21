import * as cdk from 'aws-cdk-lib';
import * as ec2 from 'aws-cdk-lib/aws-ec2';
import * as logs from 'aws-cdk-lib/aws-logs';
import * as ssm from 'aws-cdk-lib/aws-ssm';
import {Construct} from 'constructs';
import {Ec2ScriptRunner, HaproxyEc2Service} from '@aoctech/cdk';
import {Environment} from './types';

interface ApiStackProps extends cdk.StackProps {
  environment: Environment;
  vpcId: string;
  instanceProfileName: string;
  deploymentsBucketName: string;
  logsBucketName: string;
  // Bucket holding KYC identity documents. Absent → the API disables the
  // document verification path and only offers PIX-match.
  kycDocumentsBucketName: string;
  valkeyUrlSsmPath?: string;
  // Session Manager. **Off by default**: deploys replace the instances through an
  // ASG instance refresh, so nothing needs SSM RunCommand any more, and the
  // agent costs ~70 MiB of RSS on a t4g.nano. On means a shell back onto the box.
  enableSsmAgent?: boolean;
}

export class ApiStack extends cdk.Stack {
  public readonly asgName: string;

  constructor(scope: Construct, id: string, props: ApiStackProps) {
    super(scope, id, props);

    const {
      environment,
      vpcId,
      instanceProfileName,
      deploymentsBucketName,
      logsBucketName,
      kycDocumentsBucketName,
      valkeyUrlSsmPath,
      enableSsmAgent = false,
    } = props;

    const vpc = ec2.Vpc.fromLookup(this, 'Vpc', {vpcId});

    // This is the edge SG formerly attached to the shared ALB. It remains the
    // trusted source in each service SG while HAProxy takes over routing.
    const edgeSgId = ssm.StringParameter.valueForStringParameter(
      this, `/ctech/${environment}/network/alb-sg-id`,
    );
    const edgeSg = ec2.SecurityGroup.fromSecurityGroupId(this, 'EdgeSg', edgeSgId);

    const isProd = environment === 'prod';
    // Keep the existing v2 physical names and logical IDs so this migration only
    // removes ALB resources; the ASG, instances, security group, and log groups
    // are updated in place.
    const svcName = 'ctech-account';
    this.asgName = `${environment}-ctech-account`;
    const logRetention = isProd ? logs.RetentionDays.ONE_MONTH : logs.RetentionDays.ONE_WEEK;
    const logGroupApp = `/${svcName}/${environment}/app`;
    const logGroupNginx = `/${svcName}/${environment}/nginx`;

    // ── User Data ─────────────────────────────────────────────────────────────
    // Every shared bootstrap step lives in ctech-cdk's assets/ec2 and is fetched
    // from S3 at boot; the S3 key prefix is their content hash, read from SSM at
    // deploy time, so editing a shared script versions this launch template.
    const scripts = new Ec2ScriptRunner(this, 'Scripts', {environment});
    const userData = ec2.UserData.forLinux();
    scripts.install(userData);

    // setup-base.sh also creates /var/lib/ctech-account, where the MaxMind
    // GeoLite2 database is downloaded.
    scripts.run(userData, 'setup-base.sh', svcName, 'nginx');
    scripts.run(userData, 'setup-swap.sh', '256');
    scripts.run(userData, 'setup-dualstack.sh');
    scripts.run(userData, 'setup-cloudflare-ca.sh');

    // setup-base.sh installs the SSM agent and setup-dualstack.sh starts it, so
    // this is what stops it again.
    if (!enableSsmAgent) {
      userData.addCommands('systemctl disable --now amazon-ssm-agent 2>/dev/null || true');
    }

    userData.addCommands(
      `cat > /etc/app-static.env << 'ENV'`,
      `ENVIRONMENT=${environment}`,
      `TABLE_PREFIX=${environment}`,
      `AWS_REGION=${this.region}`,
      `AWS_USE_DUALSTACK_ENDPOINT=true`,
      `PORT=8000`,
      `KYC_DOCUMENTS_BUCKET=${kycDocumentsBucketName}`,
      `MAXMIND_DB_PATH=/var/lib/ctech-account/GeoLite2-City.mmdb`,
      `TRUSTED_PROXIES=127.0.0.1`,
      `ENV`,
    );

    // Secrets are read by name at service start, never embedded: the launch
    // template is readable by anyone holding ec2:DescribeLaunchTemplateVersions.
    scripts.run(userData, 'setup-ssm-env.sh',
      `SECRET_ENC_KEY=/ctech-account/${environment}/secret-encryption-key`,
      `BASE_URL=/ctech-account/${environment}/base-url`,
      `ALLOWED_ORIGINS=/ctech-account/${environment}/allowed-origins`,
      `APP_URL=/ctech-account/${environment}/app-url`,
      `WEBAUTHN_RPID=/ctech-account/${environment}/webauthn-rpid`,
      `GOOGLE_CLIENT_ID=/ctech-account/${environment}/google-client-id`,
      `GOOGLE_CLIENT_SECRET=/ctech-account/${environment}/google-client-secret`,
      `COOKIE_DOMAIN=/ctech-account/${environment}/cookie-domain`,
      `FROM_EMAIL=/ctech-account/${environment}/from-email`,
      `MAXMIND_ACCOUNT_ID=/ctech-account/${environment}/maxmind-account-id`,
      `MAXMIND_LICENSE_KEY=/ctech-account/${environment}/maxmind-license-key`,
      ...(valkeyUrlSsmPath ? [`VALKEY_URL=${valkeyUrlSsmPath}`] : []),
    );

    scripts.run(userData, 'setup-realip.sh', vpc.vpcCidrBlock);
    // 20 r/s and a 5 MB body: ctech-account's login and token routes are the
    // account-takeover surface, and its uploads are KYC documents.
    scripts.run(userData, 'setup-nginx.sh', '8080', '8000', '/v1.0/health-check', '20', '5m');
    scripts.run(userData, 'setup-app-service.sh', 'CTech Account API', 'bootstrap',
      'network.target nginx.service');
    scripts.run(userData, 'setup-deploy.sh', deploymentsBucketName, 'bootstrap',
      'http://127.0.0.1:8080/v1.0/health-check');
    scripts.run(userData, 'setup-logs.sh', logsBucketName, svcName, svcName,
      '/var/log/app', '/var/log/nginx');

    // Logs only. No `metrics` block: EC2 already publishes CPUUtilization and
    // CPUCreditBalance for free, and every custom series this service used to
    // publish was either that again or a number nobody alarmed on.
    // {instance_id} is resolved by the CW agent at runtime, not by bash.
    userData.addCommands(
      `cat > /tmp/cwagent.json << 'CWA'`,
      JSON.stringify({
        agent: {metrics_collection_interval: 60},
        logs: {
          logs_collected: {
            files: {
              collect_list: [
                {file_path: '/var/log/app/app.log', log_group_name: logGroupApp, log_stream_name: '{instance_id}'},
                {file_path: '/var/log/nginx/access.log', log_group_name: logGroupNginx, log_stream_name: '{instance_id}/access'},
                {file_path: '/var/log/nginx/error.log', log_group_name: logGroupNginx, log_stream_name: '{instance_id}/error'},
              ],
            },
          },
        },
      }),
      `CWA`,
    );
    scripts.run(userData, 'setup-cloudwatch-agent.sh', '/tmp/cwagent.json');
    scripts.run(userData, 'bootstrap-deploy.sh', deploymentsBucketName, 'ctech-account/current.zip');

    // ctech-lbalancer still owns the bootstrap route and private CNAME; this
    // service construct owns compute, logs and edge-SG ingress only.
    const service = new HaproxyEc2Service(this, 'ApiService', {
      vpc,
      edgeSecurityGroup: edgeSg,
      appPort: 8080,
      userData,
      instanceProfileName,
      securityGroupName: `${environment}-${svcName}-sg`,
      securityGroupDescription: 'ctech-account instances',
      appLogGroupName: logGroupApp,
      nginxLogGroupName: logGroupNginx,
      logRetention,
      logRemovalPolicy: isProd ? cdk.RemovalPolicy.RETAIN : cdk.RemovalPolicy.DESTROY,
      asgName: this.asgName,
      minCapacity: 1,
      maxCapacity: 1,
      // The ASG runs only inside a narrow daytime window: up at 11:55 and down
      // at 13:15 America/Sao_Paulo. Outside it the service is off — inbound
      // webhooks fail and nothing is reachable. Deliberate for a development
      // environment on a single t4g.nano.
      // schedule: {enableCron: '55 11 * * *', disableCron: '15 13 * * *'},
      spot: {

      }
    });
    new cdk.CfnOutput(this, 'AsgName', {value: service.autoScalingGroup.autoScalingGroupName, exportName: `${id}-asg-name`});
    new cdk.CfnOutput(this, 'AppLogGroupName', {
      value: service.appLogGroup.logGroupName,
      exportName: `${id}-app-log-group`,
    });
    new cdk.CfnOutput(this, 'NginxLogGroupName', {
      value: service.nginxLogGroup!.logGroupName,
      exportName: `${id}-nginx-log-group`,
    });
  }
}
