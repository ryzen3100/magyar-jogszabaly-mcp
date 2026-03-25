FROM node:22-alpine AS builder

WORKDIR /app
COPY Hungarian-law-mcp/package.json Hungarian-law-mcp/package-lock.json ./
RUN npm ci --ignore-scripts
COPY Hungarian-law-mcp/src/ ./src/
COPY Hungarian-law-mcp/tsconfig.json ./
RUN npm run build

FROM node:22-alpine

RUN addgroup -S lawmcp && adduser -S lawmcp -G lawmcp

WORKDIR /app
COPY --from=builder /app/package.json /app/package-lock.json ./
RUN npm ci --omit=dev --ignore-scripts && npm cache clean --force

COPY --from=builder /app/dist/ ./dist/

# Scripts for ingest (run as root during cron)
COPY Hungarian-law-mcp/scripts/ ./scripts/
COPY Hungarian-law-mcp/sources.yml ./
RUN npm install tsx better-sqlite3 --save-dev 2>/dev/null || true

RUN mkdir -p /data && chown lawmcp:lawmcp /data

ENV NODE_ENV=production
ENV HUNGARIAN_LAW_DB_PATH=/data/database.db
ENV PORT=3100

EXPOSE 3100

USER lawmcp
CMD ["node", "dist/http-server.js"]
