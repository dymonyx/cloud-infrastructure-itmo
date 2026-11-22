FROM postgres:17-alpine

COPY standby-entrypoint.sh /usr/local/bin/standby-entrypoint.sh

ENTRYPOINT ["/usr/local/bin/standby-entrypoint.sh"]
CMD ["postgres"]

