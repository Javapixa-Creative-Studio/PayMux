# syntax=docker/dockerfile:1

# The landing page: one self-contained HTML file, served by nginx.
#
# There is no build step because there is nothing to build. The page carries
# its own CSS and JavaScript, and the only thing it fetches from anywhere is
# its typefaces.
#
# Build from the repository root, which is where index.html lives:
#
#   docker build -f deployments/landing.Dockerfile -t paymux-landing .
#
# On Easypanel, Coolify or similar: point the service at this Dockerfile,
# leave the build context at the repository root, and bind the domain to
# container port 80.
FROM nginx:1.29-alpine

COPY deployments/nginx-security-headers.conf /etc/nginx/security-headers.conf
COPY deployments/landing.nginx.conf /etc/nginx/conf.d/default.conf
COPY index.html /usr/share/nginx/html/index.html

EXPOSE 80

# The comment above recommends deploying this image on its own, so it
# carries its own healthcheck rather than relying on docker-compose.yml.
HEALTHCHECK --interval=30s --timeout=5s --retries=3 \
  CMD ["wget", "-qO-", "http://localhost/"]
