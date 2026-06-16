FROM oven/bun:1-alpine AS build
WORKDIR /app
COPY package.json bun.lock ./
RUN bun install --frozen-lockfile
COPY . .
RUN bun run build && bun install --frozen-lockfile --production

FROM oven/bun:1-alpine
WORKDIR /app
ENV NODE_ENV=production
RUN adduser -D -u 10001 oarlock
USER oarlock
COPY --from=build /app/build ./build
COPY --from=build /app/node_modules ./node_modules
COPY --from=build /app/package.json ./
ENV PORT=3000
EXPOSE 3000
CMD ["bun", "./build/index.js"]
