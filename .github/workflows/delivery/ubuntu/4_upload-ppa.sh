function upload_ppa {
    echo "> Uploading PPA..."
    dput "ssh-ppa:bustamantejj/pack-cli" ./../*.changes
}
