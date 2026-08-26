FROM node:20-alpine AS builder
WORKDIR /app

COPY package*.json ./
RUN npm ci --ignore-scripts

COPY tsconfig.json ./
COPY src ./src
COPY scripts ./scripts
COPY data ./data
RUN npm run build \
    && test -f dist/src/http-server.js \
    && npm run build:db \
    && node --input-type=module -e "import Database from '@ansvar/mcp-sqlite'; const db = new Database('data/database.db', { readonly: true }); const tables = new Set(db.prepare(\"SELECT name FROM sqlite_master WHERE type='table'\").all().map(row => row.name)); const required = ['legal_documents', 'legal_provisions', 'provisions_fts', 'db_metadata']; if (required.some(name => !tables.has(name))) throw new Error('Generated database is missing required tables'); const documents = Number(db.prepare('SELECT COUNT(*) AS count FROM legal_documents').get().count); const provisions = Number(db.prepare('SELECT COUNT(*) AS count FROM legal_provisions').get().count); if (documents < 1 || provisions < 1) throw new Error('Generated database contains no legal data'); console.log('Validated database: ' + documents + ' documents, ' + provisions + ' provisions'); db.close();" \
    && sha256sum data/database.db | awk '{print $1}' > data/database.db.sha256

FROM node:20-alpine AS production
WORKDIR /app

ENV NODE_ENV=production \
    PORT=3000 \
    HUNGARIAN_LAW_DB_PATH=/data/database.db

RUN apk add --no-cache su-exec

COPY package*.json ./
RUN npm ci --omit=dev --ignore-scripts \
    && npm cache clean --force

COPY --from=builder /app/dist ./dist
COPY --from=builder /app/data/database.db ./dist/data/database.db
COPY --from=builder /app/data/database.db.sha256 ./dist/data/database.db.sha256
COPY icon.png ./dist/icon.png
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
