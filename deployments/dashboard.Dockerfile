# syntax=docker/dockerfile:1

# ---- build ----------------------------------------------------------------
FROM node:24-alpine AS build

WORKDIR /app

COPY apps/dashboard/package.json apps/dashboard/package-lock.json* ./
# `npm ci` and nothing else. The fallback that used to be here fired only
# when the lockfile had drifted from package.json, which is precisely when
# the build should stop rather than quietly resolve fresh versions.
RUN npm ci

COPY apps/dashboard/ ./

# Baked in at build time: Vite inlines VITE_* variables into the bundle.
ARG VITE_API_BASE_URL=http://localhost:8080
ENV VITE_API_BASE_URL=${VITE_API_BASE_URL}
RUN npm run build

# ---- runtime --------------------------------------------------------------
FROM nginx:1.29-alpine

COPY deployments/nginx-security-headers.conf /etc/nginx/security-headers.conf
COPY deployments/dashboard.nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /app/dist /usr/share/nginx/html

EXPOSE 80

# Declared here as well as in docker-compose.yml, so a platform that builds
# this Dockerfile directly still gets a liveness signal.
HEALTHCHECK --interval=15s --timeout=5s --retries=5 \
  CMD ["wget", "-qO-", "http://127.0.0.1/"]
