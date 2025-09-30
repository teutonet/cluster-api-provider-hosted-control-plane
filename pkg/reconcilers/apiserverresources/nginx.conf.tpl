events {}

pid /tmp/pid;

http {
  access_log off;
  error_log /dev/stderr warn;
  client_body_buffer_size 1000m;
  client_max_body_size 0;
  client_body_temp_path /tmp;
  proxy_temp_path /tmp;
  fastcgi_temp_path /tmp;
  uwsgi_temp_path /tmp;
  scgi_temp_path /tmp;
  proxy_request_buffering off;
  proxy_http_version 1.1;

  server {
    listen {{ .serverPort }};

    location / {
      proxy_pass {{ (index .targets 0).Url }};

      {{- with (index .targets 0).Token }}
      proxy_set_header Authorization "{{ printf "Bearer %s" .}}";
      {{- end }}

      {{- range $index, $_ := .targets }}
        {{- if gt $index 0 }}
      mirror /target_{{ $index }};
        {{- end }}
      {{- end }}
    }

    {{- range $index, $target := .targets }}
      {{- if gt $index 0 }}
    location /target_{{ $index }} {
      internal;
      proxy_pass {{ $target.Url }};
        {{- with $target.Token }}
      proxy_set_header Authorization "{{ printf "Bearer %s" .}}";
        {{- end }}
    }
      {{- end }}
    {{- end }}
  }
}
