# XPack License Monitor

A simple service that monitors elasticsearch clusters under a certain domain.
If the clusters are xpack enabled the service will list the license status an expiration.

If the license is about to expire it will be set to the license loaded into the monitoring service.

You can manage clusters being monitored by either removing them, refreshing their status or setting the license preemptively.

## Building and Running

Place your elasticsearch license file in the same directory as the docker-compose file or configure it's location.
Set the domain name of your company in the environment variables of the docker-compose file.

then run `docker-compose up -d`