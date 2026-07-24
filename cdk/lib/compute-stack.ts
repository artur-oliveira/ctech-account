import * as cdk from 'aws-cdk-lib';
import * as ec2 from 'aws-cdk-lib/aws-ec2';
import * as elbv2 from 'aws-cdk-lib/aws-elasticloadbalancingv2';
import * as logs from 'aws-cdk-lib/aws-logs';
import * as ssm from 'aws-cdk-lib/aws-ssm';
import {Construct} from 'constructs';
import {
  PrivateIpv4Ec2Service,
  addCloudWatchAgentDualStackOverride,
  addDualStackSsmAgentCommands,
  addRealipRefreshCommands,
  addSwapCommands,
} from '@aoctech/cdk';
import {Environment} from './types';

interface ComputeStackProps extends cdk.StackProps {
  environment: Environment;
  vpcId: string;
  domainName: string;
  instanceProfileName: string;
  deploymentsBucketName: string;
  logsBucketName: string;
  // Bucket holding KYC identity documents. Absent → the API disables the
  // document verification path and only offers PIX-match.
  kycDocumentsBucketName: string;
  valkeyUrlSsmPath?: string;
  // ALB listener rule priority — must not conflict with py-dfe-api (priority 15) or
  // ctech-wallet-api (priority 35). Default bumped 20 → 25: the v2 migration to
  // PrivateIpv4Ec2Service creates a new ListenerRule under a new logical ID while
  // the old one (still priority 20) is still live, and ALB rejects two rules on
  // the same listener sharing a priority.
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
      kycDocumentsBucketName,
      valkeyUrlSsmPath,
      listenerRulePriority = 25,
    } = props;

    const vpc = ec2.Vpc.fromLookup(this, 'Vpc', {vpcId});

    const albSgId = ssm.StringParameter.valueForStringParameter(
      this, `/ctech/${environment}/network/alb-sg-id`,
    );
    const albSg = ec2.SecurityGroup.fromSecurityGroupId(this, 'AlbSg', albSgId);

    const httpsListenerArn = ssm.StringParameter.valueForStringParameter(
      this, `/ctech/${environment}/alb/https-listener-arn`,
    );
    const httpsListener = elbv2.ApplicationListener.fromApplicationListenerAttributes(
      this, 'HttpsListener',
      {listenerArn: httpsListenerArn, securityGroup: albSg},
    );

    const isProd = environment === 'prod';
    // Bumped names (v2): moving the ASG/SG/log groups into PrivateIpv4Ec2Service
    // changes their CloudFormation logical IDs, which CloudFormation treats as
    // delete-old/create-new. Explicit physical names must differ from the old
    // ones or the create side of that swap collides with the still-live old
    // resource (and the listener priority bump below avoids the same collision
    // on the ALB rule).
    const svcName = 'ctech-account-v2';
    this.asgName = `${environment}-ctech-account-v2`;
    const logRetention = isProd ? logs.RetentionDays.ONE_MONTH : logs.RetentionDays.ONE_WEEK;
    const logGroupApp = `/${svcName}/${environment}/app`;
    const logGroupNginx = `/${svcName}/${environment}/nginx`;

    const userData = ec2.UserData.forLinux();

    userData.addCommands(
      'dnf install -y nginx amazon-cloudwatch-agent amazon-ssm-agent unzip jq',
      'useradd --system --no-create-home --shell /sbin/nologin webapp',
      'mkdir -p /opt/app/releases /var/log/app /etc/nginx/conf.d',
      'chown -R webapp:webapp /opt/app /var/log/app',
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
      `    # Written by /opt/app/update-realip.sh: set_real_ip_from for the ALB and for`,
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
      `    # $binary_remote_addr is the viewer's IP, not the ALB's, only because the`,
      `    # realip module rewrote it (see the include above). Without that the whole`,
      `    # req_by_ip zone collapses onto the ALB's private IP and the rate becomes a`,
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
      `BASE_URL=$(aws ssm get-parameter --name "/ctech-account/$ENVIRONMENT/base-url" --query Parameter.Value --output text --region us-east-1 2>/dev/null)`,
      `ALLOWED_ORIGINS=$(aws ssm get-parameter --name "/ctech-account/$ENVIRONMENT/allowed-origins" --query Parameter.Value --output text --region us-east-1 2>/dev/null)`,
      `APP_URL=$(aws ssm get-parameter --name "/ctech-account/$ENVIRONMENT/app-url" --query Parameter.Value --output text --region us-east-1 2>/dev/null)`,
      `GOOGLE_CLIENT_ID=$(aws ssm get-parameter --name "/ctech-account/$ENVIRONMENT/google-client-id" --query Parameter.Value --output text --region us-east-1 2>/dev/null || echo "")`,
      `GOOGLE_CLIENT_SECRET=$(aws ssm get-parameter --name "/ctech-account/$ENVIRONMENT/google-client-secret" --with-decryption --query Parameter.Value --output text --region us-east-1 2>/dev/null || echo "")`,
      `COOKIE_DOMAIN=$(aws ssm get-parameter --name "/ctech-account/$ENVIRONMENT/cookie-domain" --query Parameter.Value --output text --region us-east-1 2>/dev/null || echo "")`,
      `FROM_EMAIL=$(aws ssm get-parameter --name "/ctech-account/$ENVIRONMENT/from-email" --query Parameter.Value --output text --region us-east-1 2>/dev/null || echo "")`,
      ...(valkeyUrlSsmPath ? [
        `VALKEY_URL=$(aws ssm get-parameter --name "${valkeyUrlSsmPath}" --query Parameter.Value --output text --region us-east-1 2>/dev/null || echo "")`,
        `export VALKEY_URL`,
      ] : []),
      `export TRUSTED_PROXIES`,
      `export BASE_URL`,
      `export ALLOWED_ORIGINS`,
      `export APP_URL`,
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

    // ── Shared no-NAT-Gateway EC2/ASG pattern (@aoctech/cdk) ───────────────────
    const service = new PrivateIpv4Ec2Service(this, 'ApiService', {
      vpc,
      albSg,
      httpsListener,
      securityGroupName: `${environment}-${svcName}-sg`,
      securityGroupDescription: 'ctech-account instances',
      appPort: 8080,
      instanceProfileName,
      userData,
      logGroupAppName: logGroupApp,
      logGroupNginxName: logGroupNginx,
      logRetention,
      logRemovalPolicy: isProd ? cdk.RemovalPolicy.RETAIN : cdk.RemovalPolicy.DESTROY,
      metricNamespace: `CtechAccount/${environment}`,
      targetGroupName: `${this.asgName}-tg`,
      healthCheckPath: '/v1.0/health-check',
      healthyHttpCodes: '200',
      asgName: this.asgName,
      minCapacity: 1,
      maxCapacity: isProd ? 3 : 1,
      domainName,
      listenerRulePriority,
    });

    new cdk.CfnOutput(this, 'AsgName', {value: service.asgName, exportName: `${id}-asg-name`});
    new cdk.CfnOutput(this, 'AppLogGroupName', {
      value: service.appLogGroup.logGroupName,
      exportName: `${id}-app-log-group`,
    });
    new cdk.CfnOutput(this, 'NginxLogGroupName', {
      value: service.nginxLogGroup.logGroupName,
      exportName: `${id}-nginx-log-group`,
    });
  }
}
