# ---- build stage ----
FROM node:18-alpine AS builder

WORKDIR /app

# 复制全部源码（node_modules 由 .dockerignore 排除）
# COPY 在 npm ci 之前执行，避免 BuildKit overlay 层残留 node_modules
# 导致 "cannot replace to directory with file" 错误
COPY . .
RUN npm ci --prefer-offline
ARG BUILD_MODE=production
RUN npm run build -- --mode ${BUILD_MODE}

# ---- runtime stage ----
FROM nginx:1.25-alpine AS runtime

# 替换默认 nginx 配置
COPY nginx.conf /etc/nginx/conf.d/default.conf

# 复制构建产物
COPY --from=builder /app/dist /usr/share/nginx/html

# 非 root 用户运行
RUN chown -R nginx:nginx /usr/share/nginx/html && \
    chown -R nginx:nginx /var/cache/nginx && \
    chown -R nginx:nginx /var/log/nginx && \
    touch /var/run/nginx.pid && \
    chown nginx:nginx /var/run/nginx.pid

USER nginx

EXPOSE 80

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -qO- http://localhost/index.html || exit 1

CMD ["nginx", "-g", "daemon off;"]
