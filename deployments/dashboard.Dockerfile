# syntax=docker/dockerfile:1

# ---- build ----------------------------------------------------------------
FROM node:24-alpine AS build

WORKDIR /app

COPY apps/dashboard/package.json apps/dashboard/package-lock.json* ./
RUN npm ci || npm install

COPY apps/dashboard/ ./

# Baked in at build time: Vite inlines VITE_* variables into the bundle.
ARG VITE_API_BASE_URL=http://localhost:8080
ENV VITE_API_BASE_URL=${VITE_API_BASE_URL}
RUN npm run build

# ---- runtime --------------------------------------------------------------
FROM nginx:1.29-alpine

COPY deployments/dashboard.nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /app/dist /usr/share/nginx/html

EXPOSE 80
