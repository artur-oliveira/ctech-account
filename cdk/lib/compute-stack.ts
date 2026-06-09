import * as cdk from 'aws-cdk-lib';
import * as ec2 from 'aws-cdk-lib/aws-ec2';
import * as autoscaling from 'aws-cdk-lib/aws-autoscaling';
import {AdditionalHealthCheckType} from 'aws-cdk-lib/aws-autoscaling';
import * as elbv2 from 'aws-cdk-lib/aws-elasticloadbalancingv2';
import * as logs from 'aws-cdk-lib/aws-logs';
import * as iam from 'aws-cdk-lib/aws-iam';
import * as ssm from 'aws-cdk-lib/aws-ssm';
import {Construct} from 'constructs';
import {Environment} from './types';
import {Duration} from 'aws-cdk-lib';

interface ComputeStackProps extends cdk.StackProps {
  environment: Environment;
  vpcId: string;
  domainName: string;
  instanceProfileName: string;
  deploymentsBucketName: string;
  logsBucketName: string;
  valkeyUrlSsmPath?: string;
  // ALB listener rule priority — must not conflict with py-dfe-api (priority 10)
  listenerRulePriority?: number;
}

export class ComputeStack extends cdk.Stack {
  public readonly asgName: string;

  constructor(scope: Construct, id: string, props: ComputeStackProps) {
    super(scope, id, props);

    const {
      environment,
      vpcId,
      domainName,
      instanceProfileName,
      deploymentsBucketName,
      logsBucketName,
      valkeyUrlSsmPath,
      listenerRulePriority = 20,
    } = props;

    const vpc = ec2.Vpc.fromLookup(this, 'Vpc', {vpcId});

    const albSgId = ssm.StringParameter.valueForStringParameter(
      this, `/ctech/${environment}/network/alb-sg-id`,
    );
    const albSg = ec2.SecurityGroup.fromSecurityGroupId(this, 'AlbSg', albSgId);

    const appSg = new ec2.SecurityGroup(this, 'AppSg', {
      vpc,
      securityGroupName: `${environment}-ctech-account-sg`,
      description: 'ctech-account instances',
      allowAllOutbound: true,
      allowAllIpv6Outbound: true,
    });
    appSg.addIngressRule(albSg, ec2.Port.tcp(8080), 'ALB to app');

    const httpsListenerArn = ssm.StringParameter.valueForStringParameter(
      this, `/ctech/${environment}/alb/https-listener-arn`,
    );
    const httpsListener = elbv2.ApplicationListener.fromApplicationListenerAttributes(
      this, 'HttpsListener',
      {listenerArn: httpsListenerArn, securityGroup: albSg},
    );

    const isProd = environment === 'prod';
    this.asgName = `${environment}-ctech-account`;
    const logRetention = isProd ? logs.RetentionDays.ONE_MONTH : logs.RetentionDays.ONE_WEEK;
    const logGroupApp = `/ctech-account/${environment}/app`;
    const logGroupNginx = `/ctech-account/${environment}/nginx`;

    const appLogGroup = new logs.LogGroup(this, 'AppLogGroup', {
      logGroupName: logGroupApp,
      retention: logRetention,
      removalPolicy: isProd ? cdk.RemovalPolicy.RETAIN : cdk.RemovalPolicy.DESTROY,
    });

    const nginxLogGroup = new logs.LogGroup(this, 'NginxLogGroup', {
      logGroupName: logGroupNginx,
      retention: logRetention,
      removalPolicy: isProd ? cdk.RemovalPolicy.RETAIN : cdk.RemovalPolicy.DESTROY,
    });

    for (const [name, pattern] of [
      ['HTTP2XX', '{ ($.status >= 200) && ($.status < 300) }'],
      ['HTTP4XX', '{ ($.status >= 400) && ($.status < 500) }'],
      ['HTTP5XX', '{ $.status >= 500 }'],
    ] as [string, string][]) {
      new logs.MetricFilter(this, `${name}Filter`, {
        logGroup: nginxLogGroup,
        metricNamespace: `CtechAccount/${environment}`,
        metricName: name,
        filterPattern: logs.FilterPattern.literal(pattern),
        metricValue: '1',
        defaultValue: 0,
      });
    }

    const userData = ec2.UserData.forLinux();

    userData.addCommands(
      'dnf install -y nginx amazon-cloudwatch-agent amazon-ssm-agent unzip',
      'useradd --system --no-create-home --shell /sbin/nologin webapp',
      'mkdir -p /opt/app/releases /var/log/app',
      'chown -R webapp:webapp /opt/app /var/log/app',

      // Swap (256 MB) — prevents OOM on t4g.micro
      'if [ ! -f /var/swapfile ]; then',
      '  dd if=/dev/zero of=/var/swapfile bs=1M count=256',
      '  chmod 600 /var/swapfile',
      '  mkswap /var/swapfile',
      '  swapon /var/swapfile',
      '  echo "/var/swapfile swap swap defaults 0 0" >> /etc/fstab',
      'fi',

      'echo "AWS_USE_DUALSTACK_ENDPOINT=true" >> /etc/environment',

      `mkdir -p /etc/amazon/ssm`,
      `cat > /etc/amazon/ssm/amazon-ssm-agent.json << 'SSM'`,
      `{ "Agent": { "UseDualStackEndpoint": true } }`,
      `SSM`,
      'systemctl enable amazon-ssm-agent',
      'systemctl restart amazon-ssm-agent',

      // nginx: listens :8080, proxies to Go binary on :8000
      `cat > /etc/nginx/nginx.conf << 'NGINX'`,
      `user nginx;`,
      `pid /run/nginx.pid;`,
      `worker_processes auto;`,
      `worker_rlimit_nofile 32768;`,
      `error_log /var/log/nginx/error.log warn;`,
      ``,
      `events {`,
      `    worker_connections 4096;`,
      `    use epoll;`,
      `    multi_accept on;`,
      `}`,
      ``,
      `http {`,
      `    include /etc/nginx/mime.types;`,
      `    default_type application/octet-stream;`,
      ``,
      `    log_format json_log escape=json '{"remote_addr":"$remote_addr","status":$status,"request":"$request","body_bytes_sent":$body_bytes_sent,"request_time":$request_time}';`,
      ``,
      `    sendfile on;`,
      `    tcp_nopush on;`,
      `    tcp_nodelay on;`,
      `    keepalive_timeout 65;`,
      `    keepalive_requests 10000;`,
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
      `        keepalive 64;`,
      `    }`,
      ``,
      `    server {`,
      `        listen 8080 default_server;`,
      `        server_name _;`,
      `        access_log /var/log/nginx/access.log json_log;`,
      ``,
      `        location = /healthz {`,
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
      `            proxy_pass http://app;`,
      `            proxy_http_version 1.1;`,
      `            proxy_set_header Connection "";`,
      `            proxy_set_header Host $host;`,
      `            proxy_set_header X-Real-IP $remote_addr;`,
      `            proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;`,
      `            proxy_set_header X-Forwarded-Proto $http_x_forwarded_proto;`,
      `            proxy_connect_timeout 10s;`,
      `            proxy_read_timeout 30s;`,
      `            proxy_send_timeout 30s;`,
      `        }`,
      `    }`,
      `}`,
      `NGINX`,
      `systemctl enable nginx`,
      `systemctl start nginx`,

      // CloudWatch agent
      `mkdir -p /etc/systemd/system/amazon-cloudwatch-agent.service.d`,
      `cat > /etc/systemd/system/amazon-cloudwatch-agent.service.d/override.conf << 'CWAENV'`,
      `[Service]`,
      `Environment=AWS_USE_DUALSTACK_ENDPOINT=true`,
      `CWAENV`,

      `cat > /opt/aws/amazon-cloudwatch-agent/etc/amazon-cloudwatch-agent.json << 'CWA'`,
      `{`,
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
      `TABLE_PREFIX=${environment}_`,
      `AWS_REGION=${this.region}`,
      `AWS_USE_DUALSTACK_ENDPOINT=true`,
      `PORT=8000`,
      `ENV`,

      // start.sh: fetches secrets from SSM then execs the Go binary
      `cat > /opt/app/start.sh << 'START'`,
      `#!/bin/bash`,
      `TRUSTED_PROXIES=127.0.0.1`,
      `RSA_PRIVATE_KEY_PEM=$(aws ssm get-parameter --name "/ctech-account/$ENVIRONMENT/rsa-private-key" --with-decryption --query Parameter.Value --output text --region us-east-1 2>/dev/null || echo "")`,
      `INTERNAL_TOKEN=$(aws ssm get-parameter --name "/ctech-account/$ENVIRONMENT/internal-token" --with-decryption --query Parameter.Value --output text --region us-east-1 2>/dev/null || echo "placeholder")`,
      `PUBLIC_KEY_KID=$(aws ssm get-parameter --name "/ctech-account/$ENVIRONMENT/public-key-kid" --query Parameter.Value --output text --region us-east-1 2>/dev/null || echo "placeholder")`,
      `BASE_URL=$(aws ssm get-parameter --name "/ctech-account/$ENVIRONMENT/base-url" --query Parameter.Value --output text --region us-east-1 2>/dev/null || echo "placeholder")`,
      `ALLOWED_ORIGINS=$(aws ssm get-parameter --name "/ctech-account/$ENVIRONMENT/allowed-origins" --query Parameter.Value --output text --region us-east-1 2>/dev/null || echo "placeholder")`,
      `APP_URL=$(aws ssm get-parameter --name "/ctech-account/$ENVIRONMENT/app-url" --query Parameter.Value --output text --region us-east-1 2>/dev/null || echo "placeholder")`,
      ...(valkeyUrlSsmPath ? [
        `VALKEY_URL=$(aws ssm get-parameter --name "${valkeyUrlSsmPath}" --query Parameter.Value --output text --region us-east-1 2>/dev/null || echo "")`,
        `export VALKEY_URL`,
      ] : []),
      `export TRUSTED_PROXIES`,
      `export BASE_URL`,
      `export ALLOWED_ORIGINS`,
      `export APP_URL`,
      `export RSA_PRIVATE_KEY_PEM`,
      `export INTERNAL_TOKEN`,
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
      `  if curl -sf http://127.0.0.1:8080/healthz >/dev/null; then`,
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
      `curl -sf http://127.0.0.1:8080/healthz >/dev/null || { echo "Timed out"; exit 1; }`,
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

    const instanceProfile = iam.InstanceProfile.fromInstanceProfileName(
      this, 'InstanceProfile', instanceProfileName,
    );

    const launchTemplate = new ec2.LaunchTemplate(this, 'LaunchTemplate', {
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
      instanceProfile,
      requireImdsv2: true,
      securityGroup: appSg,
    });

    const cfnLT = launchTemplate.node.defaultChild as ec2.CfnLaunchTemplate;
    cfnLT.addPropertyDeletionOverride('LaunchTemplateData.SecurityGroupIds');
    cfnLT.addPropertyOverride('LaunchTemplateData.NetworkInterfaces', [{
      DeviceIndex: 0,
      Groups: [appSg.securityGroupId],
      AssociatePublicIpAddress: false,
      Ipv6AddressCount: 1,
    }]);

    const targetGroup = new elbv2.ApplicationTargetGroup(this, 'TargetGroup', {
      targetGroupName: `${this.asgName}-tg`,
      vpc,
      port: 8080,
      protocol: elbv2.ApplicationProtocol.HTTP,
      targetType: elbv2.TargetType.INSTANCE,
      healthCheck: {
        path: '/healthz',
        interval: cdk.Duration.seconds(15),
        timeout: cdk.Duration.seconds(5),
        healthyThresholdCount: 2,
        unhealthyThresholdCount: 5,
        healthyHttpCodes: '200',
      },
      deregistrationDelay: cdk.Duration.seconds(30),
    });

    const asg = new autoscaling.AutoScalingGroup(this, 'ASG', {
      autoScalingGroupName: this.asgName,
      vpc,
      vpcSubnets: {subnetType: ec2.SubnetType.PUBLIC},
      launchTemplate,
      minCapacity: 1,
      maxCapacity: isProd ? 3 : 1,
      cooldown: cdk.Duration.seconds(120),
      healthChecks: autoscaling.HealthChecks.withAdditionalChecks({
        additionalTypes: [AdditionalHealthCheckType.ELB],
        gracePeriod: Duration.seconds(120),
      }),
    });

    asg.attachToApplicationTargetGroup(targetGroup);

    new elbv2.ApplicationListenerRule(this, 'ListenerRule', {
      listener: httpsListener,
      priority: listenerRulePriority,
      conditions: [
        elbv2.ListenerCondition.hostHeaders([domainName]),
        elbv2.ListenerCondition.pathPatterns(['/*']),
      ],
      action: elbv2.ListenerAction.forward([targetGroup]),
    });

    new cdk.CfnOutput(this, 'AsgName', {value: this.asgName, exportName: `${id}-asg-name`});
    new cdk.CfnOutput(this, 'AppLogGroupName', {value: appLogGroup.logGroupName, exportName: `${id}-app-log-group`});
    new cdk.CfnOutput(this, 'NginxLogGroupName', {
      value: nginxLogGroup.logGroupName,
      exportName: `${id}-nginx-log-group`
    });
  }
}
