import * as cdk from 'aws-cdk-lib';
import * as ec2 from 'aws-cdk-lib/aws-ec2';
import * as logs from 'aws-cdk-lib/aws-logs';
import * as ssm from 'aws-cdk-lib/aws-ssm';
import {Construct} from 'constructs';
import {Ec2ScriptRunner, HaproxyEc2Service, SSM as CtechSSM} from '@aoctech/cdk';
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
  // Session Manager. CI deploys over SSM RunCommand (/opt/app/deploy.sh), which
  // needs the agent running. On also means a shell back onto the box.
  enableSsmAgent?: boolean;
  // 'alpine' pilots the same ctech-billing/ValkeyStackV2 custom AMI + OpenRC
  // pattern here. Default 'al2023' so every other environment/caller is
  // unaffected by this flag existing.
  osFamily?: 'al2023' | 'alpine';
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
      osFamily = 'al2023',
    } = props;
    const isAlpine = osFamily === 'alpine';

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
    // Every shared bootstrap step lives in ctech-cdk's assets/ec2(-alpine) and is
    // fetched from S3 at boot; the S3 key prefix is their content hash, read from
    // SSM at deploy time, so editing a shared script versions this launch template.
    const userData = ec2.UserData.forLinux();
    let scripts: Ec2ScriptRunner | undefined;

    if (isAlpine) {
      // No aws-cli on this AMI (musl) — ctech-ec2-agent is baked in at Packer
      // build time instead. Same ctech_run shape as ValkeyStackV2 and
      // ctech-billing's bootstrap-alpine.sh.tftpl.
      const scriptsBucket = ssm.StringParameter.valueForStringParameter(
        this, CtechSSM.ec2ScriptsAlpine(environment).bucket,
      );
      const scriptsVersion = ssm.StringParameter.valueForStringParameter(
        this, CtechSSM.ec2ScriptsAlpine(environment).version,
      );
      userData.addCommands(
        'export AWS_USE_DUALSTACK_ENDPOINT=true',
        `CTECH_SCRIPTS_BUCKET="${scriptsBucket}"`,
        `CTECH_SCRIPTS_VERSION="${scriptsVersion}"`,
        'ctech_run(){ s=$1; shift; ctech-ec2-agent s3-cp -bucket "$CTECH_SCRIPTS_BUCKET" -key "$CTECH_SCRIPTS_VERSION/$s" -dest "/tmp/$s"; bash "/tmp/$s" "$@"; }',
      );
      // setup-base.sh also creates /var/lib/ctech-account, where the MaxMind
      // GeoLite2 database is downloaded.
      userData.addCommands('ctech_run setup-base.sh ' + svcName + ' nginx nginx-openrc');
      userData.addCommands('ctech_run setup-swap.sh 256');
      userData.addCommands('ctech_run setup-dualstack.sh');
      userData.addCommands('ctech_run setup-cloudflare-ca.sh');
      // setup-base.sh installs and setup-dualstack.sh starts amazon-ssm-agent;
      // this is what stops it again, OpenRC equivalent of the AL2023 branch.
      if (!enableSsmAgent) {
        userData.addCommands('rc-service amazon-ssm-agent stop 2>/dev/null || true', 'rc-update del amazon-ssm-agent default 2>/dev/null || true');
      }
    } else {
      scripts = new Ec2ScriptRunner(this, 'Scripts', {environment});
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
    const ssmEnvArgs = [
      `SECRET_ENC_KEY=/ctech-account/${environment}/secret-encryption-key`,
      `BASE_URL=/ctech-account/${environment}/base-url`,
      `ALLOWED_ORIGINS=/ctech-account/${environment}/allowed-origins`,
      `APP_URL=/ctech-account/${environment}/app-url`,
      `WEBAUTHN_RPID=/ctech-account/${environment}/webauthn-rpid`,
      `GOOGLE_CLIENT_ID=/ctech-account/${environment}/google-client-id`,
      `GOOGLE_CLIENT_SECRET=/ctech-account/${environment}/google-client-secret`,
      `COOKIE_DOMAIN=/ctech-account/${environment}/cookie-domain`,
      `FROM_EMAIL=/ctech-account/${environment}/from-email`,
      `TURNSTILE_SECRET_KEY=/ctech-account/${environment}/turnstile-secret-key`,
      `MAXMIND_ACCOUNT_ID=/ctech-account/${environment}/maxmind-account-id`,
      `MAXMIND_LICENSE_KEY=/ctech-account/${environment}/maxmind-license-key`,
      ...(valkeyUrlSsmPath ? [`VALKEY_URL=${valkeyUrlSsmPath}`] : []),
    ];

    if (isAlpine) {
      const quoted = ssmEnvArgs.map((a) => `'${a.replace(/'/g, `'\\''`)}'`).join(' ');
      userData.addCommands(`ctech_run setup-ssm-env.sh ${quoted}`);
      userData.addCommands(`ctech_run setup-realip.sh '${vpc.vpcCidrBlock}'`);
      // 20 r/s and a 5 MB body: ctech-account's login and token routes are the
      // account-takeover surface, and its uploads are KYC documents.
      // app-port-alt (8001) turns on the zero-downtime rolling deploy: a second
      // app process nginx round-robins into, so deploy.sh can restart one unit
      // at a time instead of dropping the health check during a restart.
      userData.addCommands(`ctech_run setup-nginx.sh 8080 8000 /v1.0/health-check 20 5m 8001`);
      // Alpine's setup-app-service.sh has no After=-units argument — OpenRC
      // services here only ever declare `need net`.
      userData.addCommands(`ctech_run setup-app-service.sh 'CTech Account API' bootstrap 8001`);
      userData.addCommands(
        `ctech_run setup-deploy.sh ${deploymentsBucketName} bootstrap 'http://127.0.0.1:8080/v1.0/health-check'`,
      );
      userData.addCommands(
        `ctech_run setup-logs.sh ${logsBucketName} ${svcName} ${svcName} /var/log/app /var/log/nginx`,
      );

      // ctech-ec2-agent logs-tail replaces the CloudWatch Agent (musl has no
      // working aws-cli/CW-agent build). One logGroup per config file, so two
      // separate services + configs, same as ctech-billing's Alpine bootstrap.
      userData.addCommands(
        `cat > /tmp/ctech-logs-app.json << 'LOGSAPP'`,
        JSON.stringify({
          logGroup: logGroupApp,
          files: [
            {path: '/var/log/app/app.log', streamPrefix: 'app'},
            {path: '/var/log/app/app2.log', streamPrefix: 'app2'},
          ],
        }),
        `LOGSAPP`,
        `ctech_run setup-ctech-ec2-agent.sh /tmp/ctech-logs-app.json app`,
        `cat > /tmp/ctech-logs-nginx.json << 'LOGSNGINX'`,
        JSON.stringify({
          logGroup: logGroupNginx,
          files: [
            {path: '/var/log/nginx/access.log', streamPrefix: 'access'},
            {path: '/var/log/nginx/error.log', streamPrefix: 'error'},
          ],
        }),
        `LOGSNGINX`,
        `ctech_run setup-ctech-ec2-agent.sh /tmp/ctech-logs-nginx.json nginx`,
      );
      userData.addCommands(`ctech_run bootstrap-deploy.sh ${deploymentsBucketName} ctech-account/current.zip`);
    } else {
      scripts!.run(userData, 'setup-ssm-env.sh', ...ssmEnvArgs);
      scripts!.run(userData, 'setup-realip.sh', vpc.vpcCidrBlock);
      scripts!.run(userData, 'setup-nginx.sh', '8080', '8000', '/v1.0/health-check', '20', '5m', '8001');
      scripts!.run(userData, 'setup-app-service.sh', 'CTech Account API', 'bootstrap',
        'network.target nginx.service', '8001');
      scripts!.run(userData, 'setup-deploy.sh', deploymentsBucketName, 'bootstrap',
        'http://127.0.0.1:8080/v1.0/health-check');
      scripts!.run(userData, 'setup-logs.sh', logsBucketName, svcName, svcName,
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
                  {file_path: '/var/log/app/app2.log', log_group_name: logGroupApp, log_stream_name: '{instance_id}/app2'},
                  {file_path: '/var/log/nginx/access.log', log_group_name: logGroupNginx, log_stream_name: '{instance_id}/access'},
                  {file_path: '/var/log/nginx/error.log', log_group_name: logGroupNginx, log_stream_name: '{instance_id}/error'},
                ],
              },
            },
          },
        }),
        `CWA`,
      );
      scripts!.run(userData, 'setup-cloudwatch-agent.sh', '/tmp/cwagent.json');
      scripts!.run(userData, 'bootstrap-deploy.sh', deploymentsBucketName, 'ctech-account/current.zip');
    }

    // ctech-lbalancer still owns the bootstrap route and private CNAME; this
    // service construct owns compute, logs and edge-SG ingress only.
    const machineImage = isAlpine
      ? ec2.MachineImage.fromSsmParameter(
          CtechSSM.amiAlpine(environment).arm64,
          {os: ec2.OperatingSystemType.LINUX},
        )
      : undefined; // HaproxyEc2Service defaults to latest AL2023 arm64 minimal.

    const service = new HaproxyEc2Service(this, 'ApiService', {
      vpc,
      edgeSecurityGroup: edgeSg,
      appPort: 8080,
      userData,
      machineImage,
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
