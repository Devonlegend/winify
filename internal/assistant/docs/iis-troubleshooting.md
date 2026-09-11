# IIS Troubleshooting Guide

IIS deployments run over WinRM/PowerShell Remoting against a Windows target. The
pipeline is: sync repo, optional build, validate, stop service/app pool, back up
the live directory, copy the new files, restart the service and recycle the
application pool, then smoke test.

## WinRM connection failures

If the deploy fails before doing anything, the connection itself is the problem.
Check that `winrm_endpoint` is correct (`https://host:5986/wsman` for HTTPS,
`http://host:5985/wsman` for HTTP), that `winrm_user` is set, and that the
credential named by `credential_ref` exists. The platform supports `ntlm`
(default) and `basic` transports; set `winrm_transport` accordingly. For
self-signed WinRM certificates set `winrm_insecure: true`.

A WinRM error mentioning "connection refused" or a timeout means the WinRM
listener is not reachable: verify the firewall allows 5985/5986 and that
`winrm quickconfig` has been run on the server.

## Application pool not found

Validation fails with "IIS app pool not found" when `iis_app_pool` does not
exist on the target. Confirm the pool name with
`Get-ChildItem IIS:\AppPools` and correct `iis_app_pool` in `projects.yaml`.
Validation runs before anything live is stopped, so this failure is safe.

## web.config validation failed

Before deploying, the platform parses `web.config` as XML. If the file is
malformed the deploy stops before touching the live site. Fix the XML in the
build output. If your project does not produce a `web.config`, validation is
skipped and that is not an error.

## Site is down after a deploy

Check the smoke test result in the deploy log. The smoke test requests
`http://127.0.0.1:<port><health_path>` on the target after restart. A failure
usually means the application did not start or is listening on a different
port. Confirm the app pool started and that the site binding matches `port`.

## Restoring a previous version

Use **Roll back** on the Deployment tab. For IIS it restores the most recent
timestamped backup from `iis_backup_dir`, then restarts the service and recycles
the app pool. Backups are created automatically before every deploy.

## Missing service

If `iis_service` is set but the service does not exist, validation fails with
"service not found". Either install the service or remove `iis_service` from the
project. If no service is configured the platform only manages the app pool.
