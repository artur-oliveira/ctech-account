import * as cdk from 'aws-cdk-lib';
import * as autoscaling from 'aws-cdk-lib/aws-autoscaling';
import * as ec2 from 'aws-cdk-lib/aws-ec2';
import * as iam from 'aws-cdk-lib/aws-iam';
import * as logs from 'aws-cdk-lib/aws-logs';
import * as ssm from 'aws-cdk-lib/aws-ssm';
import {Construct} from 'constructs';
import {
  addCloudWatchAgentDualStackOverride,
  addDualStackSsmAgentCommands,
  addRealipRefreshCommands,
  addSwapCommands,
} from '@aoctech/cdk';
import {Environment} from './types';

interface ComputeStackProps extends cdk.StackProps {
  environment: Environment;
  vpcId: string;
  instanceProfileName: string;
  deploymentsBucketName: string;
  logsBucketName: string;
  // Bucket holding KYC identity documents. Absent → the API disables the
  // document verification path and only offers PIX-match.
  kycDocumentsBucketName: string;
  valkeyUrlSsmPath?: string;
}

const HTTP_STATUS_METRIC_PATTERNS: ReadonlyArray<[string, string]> = [
  ['HTTP2XX', '{ ($.status >= 200) && ($.status < 300) }'],
  ['HTTP3XX', '{ ($.status >= 300) && ($.status < 400) }'],
  ['HTTP4XX', '{ ($.status >= 400) && ($.status < 500) }'],
  ['HTTP5XX', '{ $.status >= 500 }'],
];

export class ComputeStack extends cdk.Stack {
  public readonly asgName: string;

  constructor(scope: Construct, id: string, props: ComputeStackProps) {
    super(scope, id, props);

    const {
      environment,
      vpcId,
      instanceProfileName,
      deploymentsBucketName,
      logsBucketName,
      kycDocumentsBucketName,
      valkeyUrlSsmPath,
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

    const userData = ec2.UserData.forLinux();

    userData.addCommands(
      'dnf install -y nginx amazon-cloudwatch-agent amazon-ssm-agent cronie unzip jq',
      'useradd --system --no-create-home --shell /sbin/nologin webapp',
      'mkdir -p /opt/app/releases /var/log/app /etc/nginx/conf.d',
      'chown -R webapp:webapp /opt/app /var/log/app',
      // AL2023 does not enable crond by default (unlike AL2) — without it
      // /etc/cron.daily/logrotate never fires and rotated logs never reach S3.
      'systemctl enable crond',
      'systemctl start crond',
    );

    addSwapCommands(userData);
    addDualStackSsmAgentCommands(userData);

    userData.addCommands(
      // nginx: listens :8080, proxies to Go binary on :8000
      `cat > /etc/nginx/nginx.conf << 'NGINX'`,
      `user nginx;`,
      `pid /run/nginx.pid;`,
      `worker_processes auto;`,
      `worker_rlimit_nofile 65535;`,
      `error_log /var/log/nginx/error.log warn;`,
      ``,
      `events {`,
      `    worker_connections 8192;`,
      `    use epoll;`,
      `    multi_accept on;`,
      `}`,
      ``,
      `http {`,
      `    include /etc/nginx/mime.types;`,
      `    default_type application/octet-stream;`,
      ``,
      `    # Written by /opt/app/update-realip.sh: set_real_ip_from for HAProxy and for`,
      `    # CloudFront's origin-facing ranges, so $remote_addr below is the real viewer`,
      `    # IP and not the proxy's. The glob keeps nginx bootable if the file is absent.`,
      `    include /etc/nginx/conf.d/realip*.conf;`,
      ``,
      `    log_format json_log escape=json '{"remote_addr":"$remote_addr","status":$status,"request":"$request","body_bytes_sent":$body_bytes_sent,"request_time":$request_time}';`,
      ``,
      `    sendfile on;`,
      `    tcp_nopush on;`,
      `    tcp_nodelay on;`,
      `    keepalive_timeout 30;`,
      `    keepalive_requests 10000;`,
      `    reset_timedout_connection on;`,
      `    open_file_cache max=1000 inactive=20s;`,
      `    open_file_cache_valid 30s;`,
      `    open_file_cache_min_uses 2;`,
      `    open_file_cache_errors on;`,
      ``,
      `    # $binary_remote_addr is the viewer's IP, not HAProxy's, only because the`,
      `    # realip module rewrote it (see the include above). Without that the whole`,
      `    # req_by_ip zone collapses onto HAProxy's private IP and the rate becomes a`,
      `    # shared ceiling for every client at once — on the login and token routes.`,
      `    limit_req_zone  $binary_remote_addr zone=req_by_ip:10m  rate=20r/s;`,
      `    limit_conn_zone $binary_remote_addr zone=conn_by_ip:10m;`,
      `    limit_req_status  429;`,
      `    limit_conn_status 429;`,
      ``,
      `    client_max_body_size 5m;`,
      `    gzip on;`,
      `    gzip_types application/json application/javascript text/plain text/css;`,
      `    server_tokens off;`,
      `    add_header X-Content-Type-Options nosniff always;`,
      `    add_header X-Frame-Options DENY always;`,
      ``,
      `    upstream app {`,
      `        server 127.0.0.1:8000;`,
      `        keepalive 256;`,
      `        keepalive_requests 10000;`,
      `        keepalive_timeout 60s;`,
      `    }`,
      ``,
      `    server {`,
      `        listen 8080 default_server reuseport;`,
      `        server_name _;`,
      `        access_log /var/log/nginx/access.log json_log;`,
      ``,
      `        location = /v1.0/health-check {`,
      `            proxy_pass http://app;`,
      `            proxy_http_version 1.1;`,
      `            proxy_set_header Connection "";`,
      `            proxy_set_header Host $host;`,
      `            proxy_connect_timeout 5s;`,
      `            proxy_read_timeout 5s;`,
      `            access_log off;`,
      `        }`,
      ``,
      `        location / {`,
      `            limit_req  zone=req_by_ip  burst=200 nodelay;`,
      `            limit_conn conn_by_ip 100;`,
      ``,
      `            proxy_pass http://app;`,
      `            proxy_http_version 1.1;`,
      `            proxy_set_header Connection "";`,
      `            proxy_set_header Host $host;`,
      `            proxy_set_header X-Real-IP $remote_addr;`,
      // Overwrite rather than append: $proxy_add_x_forwarded_for would carry through
      // whatever X-Forwarded-For the client sent, and the Go app trusts the leftmost
      // entry. $remote_addr is the realip-resolved viewer IP, which a client cannot forge.
      `            proxy_set_header X-Forwarded-For $remote_addr;`,
      `            proxy_set_header X-Forwarded-Proto $http_x_forwarded_proto;`,
      `            proxy_connect_timeout 10s;`,
      `            proxy_read_timeout 30s;`,
      `            proxy_send_timeout 30s;`,
      `        }`,
      `    }`,
      `}`,
      `NGINX`,
    );

    addRealipRefreshCommands(userData, vpc.vpcCidrBlock);

    userData.addCommands(
      'systemctl enable nginx',
      'systemctl start nginx',
    );

    addCloudWatchAgentDualStackOverride(userData);

    userData.addCommands(
      `cat > /opt/aws/amazon-cloudwatch-agent/etc/amazon-cloudwatch-agent.json << 'CWA'`,
      `{`,
      `  "agent": {"metrics_collection_interval": 60},`,
      `  "metrics": {`,
      `    "namespace": "CtechAccount/${environment}/Host",`,
      '    "append_dimensions": {"InstanceId": "${aws:InstanceId}"},',
      `    "metrics_collected": {`,
      `      "mem": {"measurement":["used_percent"],"metrics_collection_interval":60},`,
      `      "swap": {"measurement":["used_percent"],"metrics_collection_interval":60},`,
      `      "disk": {"measurement":["used_percent"],"resources":["/"],"drop_device":true,"metrics_collection_interval":60},`,
      `      "procstat": [{"pattern":"/opt/app/current/(app|bootstrap)","measurement":["memory_rss"],"metrics_collection_interval":60}]`,
      `    }`,
      `  },`,
      `  "logs": {`,
      `    "logs_collected": {`,
      `      "files": {`,
      `        "collect_list": [`,
      `          {"file_path":"/var/log/app/app.log","log_group_name":"${logGroupApp}","log_stream_name":"{instance_id}"},`,
      `          {"file_path":"/var/log/nginx/access.log","log_group_name":"${logGroupNginx}","log_stream_name":"{instance_id}/access"},`,
      `          {"file_path":"/var/log/nginx/error.log","log_group_name":"${logGroupNginx}","log_stream_name":"{instance_id}/error"}`,
      `        ]`,
      `      }`,
      `    }`,
      `  }`,
      `}`,
      `CWA`,
      `/opt/aws/amazon-cloudwatch-agent/bin/amazon-cloudwatch-agent-ctl -a fetch-config -m ec2 -c file:/opt/aws/amazon-cloudwatch-agent/etc/amazon-cloudwatch-agent.json -s`,

      // Static env file
      `cat > /etc/app-static.env << 'ENV'`,
      `ENVIRONMENT=${environment}`,
      `TABLE_PREFIX=${environment}`,
      `AWS_REGION=${this.region}`,
      `AWS_USE_DUALSTACK_ENDPOINT=true`,
      `PORT=8000`,
      `KYC_DOCUMENTS_BUCKET=${kycDocumentsBucketName}`,
      `ENV`,

      // start.sh: fetches secrets from SSM then execs the Go binary
      `cat > /opt/app/start.sh << 'START'`,
      `#!/bin/bash`,
      // APP_VERSION is shipped inside the release artifact (release.env) by CI/CD.
      // Format: YYMMDDHHMM:<7-char commit>. Surfaced as releaseId on the health check.
      `if [ -f /opt/app/current/release.env ]; then set -a; . /opt/app/current/release.env; set +a; fi`,
      `TRUSTED_PROXIES=127.0.0.1`,
      `INTERNAL_TOKEN=$(aws ssm get-parameter --name "/ctech-account/$ENVIRONMENT/internal-token" --with-decryption --query Parameter.Value --output text --region us-east-1 2>/dev/null || echo "placeholder")`,
      `BASE_URL=$(aws ssm get-parameter --name "/ctech-account/$ENVIRONMENT/base-url" --with-decryption --query Parameter.Value --output text --region us-east-1 2>/dev/null)`,
      `ALLOWED_ORIGINS=$(aws ssm get-parameter --name "/ctech-account/$ENVIRONMENT/allowed-origins" --with-decryption --query Parameter.Value --output text --region us-east-1 2>/dev/null)`,
      `APP_URL=$(aws ssm get-parameter --name "/ctech-account/$ENVIRONMENT/app-url" --with-decryption --query Parameter.Value --output text --region us-east-1 2>/dev/null)`,
      // Optional shared-domain override. When absent, the API safely derives
      // the WebAuthn RP ID from APP_URL's hostname.
      `WEBAUTHN_RPID=$(aws ssm get-parameter --name "/ctech-account/$ENVIRONMENT/webauthn-rpid" --with-decryption --query Parameter.Value --output text --region us-east-1 2>/dev/null || echo "")`,
      `GOOGLE_CLIENT_ID=$(aws ssm get-parameter --name "/ctech-account/$ENVIRONMENT/google-client-id" --with-decryption --query Parameter.Value --output text --region us-east-1 2>/dev/null || echo "")`,
      `GOOGLE_CLIENT_SECRET=$(aws ssm get-parameter --name "/ctech-account/$ENVIRONMENT/google-client-secret" --with-decryption --query Parameter.Value --output text --region us-east-1 2>/dev/null || echo "")`,
      `COOKIE_DOMAIN=$(aws ssm get-parameter --name "/ctech-account/$ENVIRONMENT/cookie-domain" --with-decryption --query Parameter.Value --output text --region us-east-1 2>/dev/null || echo "")`,
      `FROM_EMAIL=$(aws ssm get-parameter --name "/ctech-account/$ENVIRONMENT/from-email" --with-decryption --query Parameter.Value --output text --region us-east-1 2>/dev/null || echo "")`,
      ...(valkeyUrlSsmPath ? [
        `VALKEY_URL=$(aws ssm get-parameter --name "${valkeyUrlSsmPath}" --with-decryption --query Parameter.Value --output text --region us-east-1 2>/dev/null || echo "")`,
        `export VALKEY_URL`,
      ] : []),
      `export TRUSTED_PROXIES`,
      `export BASE_URL`,
      `export ALLOWED_ORIGINS`,
      `export APP_URL`,
      `export WEBAUTHN_RPID`,
      `export INTERNAL_TOKEN`,
      `export GOOGLE_CLIENT_ID`,
      `export GOOGLE_CLIENT_SECRET`,
      `export COOKIE_DOMAIN`,
      `export FROM_EMAIL`,
      `exec /opt/app/current/bootstrap >> /var/log/app/app.log 2>&1`,
      `START`,
      `chmod +x /opt/app/start.sh`,

      // systemd app.service
      `cat > /etc/systemd/system/app.service << 'SVC'`,
      `[Unit]`,
      `Description=ctech-account`,
      `After=network.target nginx.service`,
      `StartLimitIntervalSec=300`,
      `StartLimitBurst=5`,
      ``,
      `[Service]`,
      `User=webapp`,
      `Group=webapp`,
      `WorkingDirectory=/opt/app/current`,
      `Environment=HOME=/opt/app`,
      `EnvironmentFile=/etc/app-static.env`,
      `ExecStartPre=/bin/test -x /opt/app/current/bootstrap`,
      `ExecStart=/opt/app/start.sh`,
      `Restart=on-failure`,
      `RestartSec=30`,
      ``,
      `[Install]`,
      `WantedBy=multi-user.target`,
      `SVC`,
      `systemctl daemon-reload`,
      `systemctl enable app`,

      // deploy.sh: called by SSM RunCommand from GitHub Actions
      `cat > /opt/app/deploy.sh << 'DEPLOY'`,
      `#!/bin/bash`,
      `set -euo pipefail`,
      `S3_KEY="$1"`,
      `RELEASE_DIR="/opt/app/releases/$(date +%Y%m%d_%H%M%S)"`,
      `mkdir -p "$RELEASE_DIR"`,
      `echo "Downloading release: $S3_KEY"`,
      `aws s3 cp "s3://__BUCKET__/$S3_KEY" /tmp/release.zip`,
      `unzip -o /tmp/release.zip -d "$RELEASE_DIR"`,
      `chmod +x "$RELEASE_DIR/bootstrap"`,
      `chown -R webapp:webapp "$RELEASE_DIR"`,
      `ln -sfT "$RELEASE_DIR" /opt/app/current`,
      `systemctl restart app 2>/dev/null || systemctl start app`,
      `for i in {1..60}; do`,
      `  if curl -sf http://127.0.0.1:8080/v1.0/health-check >/dev/null; then`,
      `    echo "Health check passed"`,
      `    break`,
      `  fi`,
      `  if systemctl is-failed --quiet app; then`,
      `    echo "Application failed to start"`,
      `    journalctl -u app --no-pager -n 100 || true`,
      `    exit 1`,
      `  fi`,
      `  sleep 2`,
      `done`,
      `curl -sf http://127.0.0.1:8080/v1.0/health-check >/dev/null || { echo "Timed out"; exit 1; }`,
      `ls -dt /opt/app/releases/*/ 2>/dev/null | tail -n +2 | xargs rm -rf 2>/dev/null || true`,
      `echo "Deployment successful"`,
      `DEPLOY`,
      `sed -i 's|__BUCKET__|${deploymentsBucketName}|g' /opt/app/deploy.sh`,
      `chmod +x /opt/app/deploy.sh`,

      // upload-logs.sh
      `cat > /opt/app/upload-logs.sh << 'UPLOAD'`,
      `#!/bin/bash`,
      `TOKEN=$(curl -sf -X PUT "http://169.254.169.254/latest/api/token" -H "X-aws-ec2-metadata-token-ttl-seconds: 60")`,
      `INSTANCE_ID=$(curl -sf -H "X-aws-ec2-metadata-token: $TOKEN" "http://169.254.169.254/latest/meta-data/instance-id" || echo "unknown")`,
      `DATE=$(date +%Y%m%d)`,
      `BUCKET="__LOG_BUCKET__"`,
      `ARCHIVE="/tmp/\${DATE}-\${INSTANCE_ID}.tar.gz"`,
      `ROTATED=$(find /var/log/app /var/log/nginx -name "*-\${DATE}.gz" 2>/dev/null)`,
      `[ -z "$ROTATED" ] && exit 0`,
      `tar czf "$ARCHIVE" $ROTATED 2>/dev/null || exit 0`,
      `aws s3 cp "$ARCHIVE" "s3://\${BUCKET}/ctech-account/\${DATE}-\${INSTANCE_ID}.tar.gz" --region us-east-1 || exit 0`,
      `find /var/log/app /var/log/nginx -name "*-\${DATE}.gz" -delete`,
      `rm -f "$ARCHIVE"`,
      `UPLOAD`,
      `sed -i 's|__LOG_BUCKET__|${logsBucketName}|g' /opt/app/upload-logs.sh`,
      `chmod +x /opt/app/upload-logs.sh`,

      // logrotate
      `cat > /etc/logrotate.d/ctech-account << 'LOGROTATE'`,
      `/var/log/app/app.log`,
      `/var/log/nginx/access.log`,
      `/var/log/nginx/error.log {`,
      `    daily`,
      `    compress`,
      `    copytruncate`,
      `    missingok`,
      `    notifempty`,
      `    dateext`,
      `    dateformat -%Y%m%d`,
      `    rotate 1`,
      `    sharedscripts`,
      `    postrotate`,
      `        /opt/app/upload-logs.sh`,
      `    endscript`,
      `}`,
      `LOGROTATE`,

      // Bootstrap: deploy if artifact exists
      `aws s3api head-object --bucket "${deploymentsBucketName}" --key "ctech-account/current.zip" 2>/dev/null && /opt/app/deploy.sh ctech-account/current.zip || echo "No bootstrap artifact, waiting for first deploy"`,
    );

    // HAProxy discovers healthy ASG members from the account route supplied by
    // ctech-lbalancer's default registrations.
    // Do not use PrivateIpv4Ec2Service here: it creates an ALB target group and
    // listener rule, both of which are retired by this migration.
    const serviceSg = new ec2.SecurityGroup(this, 'ApiServiceSg', {
      vpc,
      securityGroupName: `${environment}-${svcName}-sg`,
      description: 'ctech-account instances',
      allowAllOutbound: true,
      allowAllIpv6Outbound: true,
    });
    serviceSg.addIngressRule(edgeSg, ec2.Port.tcp(8080), 'HAProxy edge to app');

    const appLogGroup = new logs.LogGroup(this, 'ApiServiceAppLogGroup', {
      logGroupName: logGroupApp,
      retention: logRetention,
      removalPolicy: isProd ? cdk.RemovalPolicy.RETAIN : cdk.RemovalPolicy.DESTROY,
    });
    const nginxLogGroup = new logs.LogGroup(this, 'ApiServiceNginxLogGroup', {
      logGroupName: logGroupNginx,
      retention: logRetention,
      removalPolicy: isProd ? cdk.RemovalPolicy.RETAIN : cdk.RemovalPolicy.DESTROY,
    });
    for (const [name, pattern] of HTTP_STATUS_METRIC_PATTERNS) {
      new logs.MetricFilter(this, `ApiService${name}Filter`, {
        logGroup: nginxLogGroup,
        metricNamespace: `CtechAccount/${environment}`,
        metricName: name,
        filterPattern: logs.FilterPattern.literal(pattern),
        metricValue: '1',
        defaultValue: 0,
      });
    }

    const launchTemplate = new ec2.LaunchTemplate(this, 'ApiServiceLaunchTemplate', {
      launchTemplateName: `${this.asgName}-lt`,
      instanceType: ec2.InstanceType.of(ec2.InstanceClass.T4G, ec2.InstanceSize.MICRO),
      machineImage: ec2.MachineImage.latestAmazonLinux2023({
        cpuType: ec2.AmazonLinuxCpuType.ARM_64,
        edition: ec2.AmazonLinuxEdition.MINIMAL,
      }),
      blockDevices: [{
        deviceName: '/dev/xvda',
        volume: ec2.BlockDeviceVolume.ebs(3, {
          volumeType: ec2.EbsDeviceVolumeType.GP3,
          deleteOnTermination: true,
        }),
      }],
      userData,
      instanceProfile: iam.InstanceProfile.fromInstanceProfileName(
        this, 'ApiServiceInstanceProfile', instanceProfileName,
      ),
      requireImdsv2: true,
      securityGroup: serviceSg,
    });
    const cfnLaunchTemplate = launchTemplate.node.defaultChild as ec2.CfnLaunchTemplate;
    cfnLaunchTemplate.addPropertyDeletionOverride('LaunchTemplateData.SecurityGroupIds');
    cfnLaunchTemplate.addPropertyOverride('LaunchTemplateData.NetworkInterfaces', [{
      DeviceIndex: 0,
      Groups: [serviceSg.securityGroupId],
      AssociatePublicIpAddress: false,
      Ipv6AddressCount: 1,
    }]);

    const asg = new autoscaling.AutoScalingGroup(this, 'ApiServiceASG', {
      autoScalingGroupName: this.asgName,
      vpc,
      vpcSubnets: {subnetType: ec2.SubnetType.PUBLIC},
      launchTemplate,
      minCapacity: 1,
      maxCapacity: isProd ? 3 : 1,
      cooldown: cdk.Duration.seconds(120),
      healthChecks: autoscaling.HealthChecks.ec2({gracePeriod: cdk.Duration.seconds(120)}),
    });
    if (isProd) {
      asg.scaleOnCpuUtilization('ApiServiceCpuTargetTracking', {
        targetUtilizationPercent: 60,
        cooldown: cdk.Duration.minutes(3),
      });
    }

    new cdk.CfnOutput(this, 'AsgName', {value: asg.autoScalingGroupName, exportName: `${id}-asg-name`});
    new cdk.CfnOutput(this, 'AppLogGroupName', {
      value: appLogGroup.logGroupName,
      exportName: `${id}-app-log-group`,
    });
    new cdk.CfnOutput(this, 'NginxLogGroupName', {
      value: nginxLogGroup.logGroupName,
      exportName: `${id}-nginx-log-group`,
    });
  }
}
