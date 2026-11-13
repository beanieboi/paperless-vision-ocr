# nix-darwin module: runs paperless-vision-ocr as a launchd daemon.
#
# A daemon rather than a user agent: after a reboot with FileVault unlocked
# remotely nobody logs in to the GUI, so agents would never start. Vision
# works without a login session.
self:
{
  config,
  lib,
  pkgs,
  ...
}:
let
  cfg = config.services.paperless-vision-ocr;
  inherit (lib) mkOption types literalExpression;
  home = "/Users/${cfg.user}";
in
{
  options.services.paperless-vision-ocr = {
    enable = lib.mkEnableOption "paperless-vision-ocr, an Azure Document Intelligence-compatible OCR service for Paperless-ngx";

    package = mkOption {
      type = types.package;
      default = self.packages.${pkgs.stdenv.hostPlatform.system}.default;
      defaultText = literalExpression "paperless-vision-ocr.packages.\${system}.default";
      description = "Package to run. Its default MAC_OCR_PATH points at the pinned mac-ocr build, available as `package.mac-ocr`.";
    };

    user = mkOption {
      type = types.str;
      description = "User the daemon runs as.";
    };

    group = mkOption {
      type = types.str;
      default = "staff";
      description = "Group the daemon runs as.";
    };

    host = mkOption {
      type = types.str;
      default = "0.0.0.0";
      description = "Listen address.";
    };

    port = mkOption {
      type = types.port;
      default = 8080;
      description = "Listen port.";
    };

    languages = mkOption {
      type = types.nonEmptyListOf types.str;
      default = [
        "de-DE"
        "en-US"
      ];
      description = "BCP-47 recognition language hints, checked against `mac-ocr languages` at startup.";
    };

    strategy = mkOption {
      type = types.enum [
        "auto"
        "standard"
        "partitioned"
      ];
      default = "auto";
      description = "mac-ocr OCR strategy.";
    };

    maxConcurrentJobs = mkOption {
      type = types.ints.between 1 128;
      default = 1;
      description = "Concurrent Apple Vision jobs.";
    };

    maxQueuedJobs = mkOption {
      type = types.ints.between 1 10000;
      default = 20;
      description = "Pending-job capacity.";
    };

    maxUploadMB = mkOption {
      type = types.ints.between 1 10240;
      default = 100;
      description = "Upload size limit in MiB.";
    };

    timeoutMinutes = mkOption {
      type = types.ints.between 1 1440;
      default = 30;
      description = "Total timeout per OCR job.";
    };

    stallTimeoutSeconds = mkOption {
      type = types.ints.unsigned;
      default = 120;
      description = ''
        Kill a mac-ocr process whose CPU time has not advanced for this long.
        Vision occasionally never returns after the Neural Engine finished,
        which would otherwise block the queue until `timeoutMinutes`.
        0 disables the check; otherwise at least 10.
      '';
    };

    jobTTLHours = mkOption {
      type = types.ints.between 1 8760;
      default = 24;
      description = "How long finished jobs and their PDFs are kept.";
    };

    logLevel = mkOption {
      type = types.enum [
        "DEBUG"
        "INFO"
        "WARN"
        "ERROR"
      ];
      default = "INFO";
      description = "Log level.";
    };

    workDir = mkOption {
      type = types.str;
      default = "${home}/Library/Caches/paperless-vision-ocr";
      defaultText = literalExpression ''"/Users/''${user}/Library/Caches/paperless-vision-ocr"'';
      description = "Job working directory (created with mode 0700).";
    };

    logDir = mkOption {
      type = types.str;
      default = "${home}/Library/Logs";
      defaultText = literalExpression ''"/Users/''${user}/Library/Logs"'';
      description = "Directory for paperless-vision-ocr.log and paperless-vision-ocr.error.log.";
    };

    environment = mkOption {
      type = types.attrsOf types.str;
      default = { };
      example = {
        MAC_OCR_PATH = "/usr/local/bin/mac-ocr";
      };
      description = ''
        Extra environment variables. They end up in a world-readable plist
        under /Library/LaunchDaemons, so don't put OCR_API_KEY here unless
        that is acceptable.
      '';
    };
  };

  config = lib.mkIf cfg.enable {
    assertions = [
      {
        assertion = cfg.stallTimeoutSeconds == 0 || cfg.stallTimeoutSeconds >= 10;
        message = "services.paperless-vision-ocr.stallTimeoutSeconds must be 0 or at least 10.";
      }
    ];

    launchd.daemons.paperless-vision-ocr = {
      # `command` (not ProgramArguments) waits for /nix/store to be mounted.
      command = lib.getExe cfg.package;
      serviceConfig = {
        UserName = cfg.user;
        GroupName = cfg.group;
        EnvironmentVariables = {
          HOME = home;
          HOST = cfg.host;
          PORT = toString cfg.port;
          LOG_LEVEL = cfg.logLevel;
          OCR_LANGUAGES = lib.concatStringsSep "," cfg.languages;
          OCR_STRATEGY = cfg.strategy;
          OCR_MAX_CONCURRENT_JOBS = toString cfg.maxConcurrentJobs;
          OCR_MAX_QUEUED_JOBS = toString cfg.maxQueuedJobs;
          OCR_MAX_UPLOAD_MB = toString cfg.maxUploadMB;
          OCR_TIMEOUT_MINUTES = toString cfg.timeoutMinutes;
          OCR_STALL_TIMEOUT_SECONDS = toString cfg.stallTimeoutSeconds;
          OCR_JOB_TTL_HOURS = toString cfg.jobTTLHours;
          OCR_WORK_DIR = cfg.workDir;
        }
        // cfg.environment;
        RunAtLoad = true;
        KeepAlive.SuccessfulExit = false;
        ThrottleInterval = 10;
        ProcessType = "Background";
        Umask = 63; # 077
        StandardOutPath = "${cfg.logDir}/paperless-vision-ocr.log";
        StandardErrorPath = "${cfg.logDir}/paperless-vision-ocr.error.log";
      };
    };
  };
}
