FROM oven/bun:1-alpine AS build
WORKDIR /app
COPY package.json bun.lock ./
RUN bun install --frozen-lockfile
COPY . .
RUN bun run build
# Reduce to production dependencies for the runtime image. --ignore-scripts
# skips the `prepare` (svelte-kit sync) lifecycle hook, which needs devDeps and
# already ran during the full install above.
RUN rm -rf node_modules && bun install --frozen-lockfile --production --ignore-scripts

FROM node:24-alpine
WORKDIR /app
ENV NODE_ENV=production
RUN adduser -D -u 10001 oarlock
USER oarlock
COPY --from=build /app/build ./build
COPY --from=build /app/node_modules ./node_modules
COPY --from=build /app/package.json ./
ENV PORT=3000
EXPOSE 3000
CMD ["node", "build"]
