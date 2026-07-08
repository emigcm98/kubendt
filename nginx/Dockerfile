FROM node:24.13-alpine AS build

WORKDIR /app

COPY frontend/package.json frontend/package-lock.json* ./
RUN npm install -g npm@11.8.0 && npm install

COPY frontend/ .

ARG REACT_APP_API_BASE_URL
ENV REACT_APP_API_BASE_URL=${REACT_APP_API_BASE_URL}

RUN npm run build

# Unprivileged nginx: runs as the non-root "nginx" user (UID 101) and listens
# on 8080 instead of the privileged port 80.
FROM nginxinc/nginx-unprivileged:1.31-alpine

COPY --from=build /app/build /usr/share/nginx/html
COPY nginx/kubendt.conf /etc/nginx/conf.d/default.conf

EXPOSE 8080

CMD ["nginx", "-g", "daemon off;"]
