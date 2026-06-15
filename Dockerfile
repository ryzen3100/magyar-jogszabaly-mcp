FROM node:20-alpine AS builder
WORKDIR /app

COPY package*.json ./
RUN npm ci --ignore-scripts

COPY tsconfig.json ./
COPY src ./src
COPY scripts ./scripts
RUN npm run build \
    && test -f dist/src/http-server.js

FROM node:20-alpine AS production
WORKDIR /app

ENV NODE_ENV=production \
    PORT=3000 \
    HUNGARIAN_LAW_DB_PATH=/data/database.db \
    REBUILD_DB_ON_FIRST_RUN=false

RUN apk add --no-cache libstdc++ su-exec \
    && apk add --no-cache --virtual .native-build-deps python3 make g++

COPY package*.json ./
RUN npm ci --omit=dev --ignore-scripts \
    && npm install --save-prod --package-lock=false better-sqlite3@12.6.2 \
    && node -e "import('better-sqlite3').then(() => console.log('better-sqlite3 available'))" \
    && npm cache clean --force \
    && apk del .native-build-deps

COPY --from=builder /app/dist ./dist
COPY package.json ./dist/package.json
COPY data ./dist/data
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh

RUN addgroup -S nodejs \
    && adduser -S nodejs -G nodejs \
    && mkdir -p /data \
    && chmod +x /usr/local/bin/docker-entrypoint.sh \
    && chown -R nodejs:nodejs /app /data

EXPOSE 3000

HEALTHCHECK --interval=30s --timeout=10s --start-period=30s --retries=5 \
  CMD node -e "fetch('http://127.0.0.1:' + (process.env.PORT || 3000) + '/health').then(r => process.exit(r.ok ? 0 : 1)).catch(() => process.exit(1))"

ENTRYPOINT ["docker-entrypoint.sh"]
CMD ["node", "dist/src/http-server.js"]
