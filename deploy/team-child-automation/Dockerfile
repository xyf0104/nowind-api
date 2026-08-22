FROM mcr.microsoft.com/playwright:v1.55.0-noble

WORKDIR /app
COPY package.json ./package.json
RUN npm install --omit=dev
COPY server.mjs ./server.mjs

ENV NODE_ENV=production
EXPOSE 8090
CMD ["node", "server.mjs"]
