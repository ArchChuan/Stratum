# ---- build stage ----
FROM node:18-alpine AS builder

WORKDIR /app

# 依赖文件先 COPY,npm ci 层可独立缓存(源码变更不破坏该层);
# 随后 COPY 源码(node_modules/dist 由 .dockerignore 排除,不再与
# npm ci 生成的 node_modules 层产生 overlay 冲突),最后构建。
COPY package.json package-lock.json ./
RUN npm ci --prefer-offline
COPY . .
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
